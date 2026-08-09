package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"time"

	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	"github.com/coder/websocket"
)

var ErrOutboundQueueFull = errors.New("Relay outbound queue is full")

type Config struct {
	URL             string
	MaxMessageBytes int64
	QueueSize       int
	PingInterval    time.Duration
	MinBackoff      time.Duration
	MaxBackoff      time.Duration
	HTTPClient      *http.Client
}

type InitialMessages func() []protocol.Message
type MessageHandler func(context.Context, protocol.Message)

type Client struct {
	config   Config
	logger   *slog.Logger
	outbound chan protocol.Message
}

func New(config Config, logger *slog.Logger) *Client {
	if config.MaxMessageBytes < 1 {
		config.MaxMessageBytes = 64 * 1024
	}
	if config.QueueSize < 1 {
		config.QueueSize = 128
	}
	if config.PingInterval <= 0 {
		config.PingInterval = 20 * time.Second
	}
	if config.MinBackoff <= 0 {
		config.MinBackoff = time.Second
	}
	if config.MaxBackoff < config.MinBackoff {
		config.MaxBackoff = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{config: config, logger: logger, outbound: make(chan protocol.Message, config.QueueSize)}
}

func (c *Client) Publish(message protocol.Message) error {
	select {
	case c.outbound <- message:
		return nil
	default:
		return ErrOutboundQueueFull
	}
}

func (c *Client) Run(ctx context.Context, initial InitialMessages, handler MessageHandler) error {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := c.runSession(ctx, initial, handler)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.logger.Warn("Relay connection lost", "error", err)
		delay := c.backoff(attempt)
		attempt++
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) runSession(parent context.Context, initial InitialMessages, handler MessageHandler) error {
	dialOptions := &websocket.DialOptions{HTTPClient: c.config.HTTPClient}
	connection, _, err := websocket.Dial(parent, c.config.URL, dialOptions)
	if err != nil {
		return fmt.Errorf("connect to Relay: %w", err)
	}
	defer connection.CloseNow()
	connection.SetReadLimit(c.config.MaxMessageBytes)
	c.drainOutbound()
	for _, message := range initial() {
		if err := c.write(parent, connection, message); err != nil {
			return err
		}
	}
	c.logger.Info("Connected to Relay", "url", c.config.URL)

	sessionContext, cancel := context.WithCancel(parent)
	defer cancel()
	errorsChannel := make(chan error, 2)
	go func() { errorsChannel <- c.readLoop(sessionContext, connection, handler) }()
	go func() { errorsChannel <- c.writeLoop(sessionContext, connection) }()
	firstErr := <-errorsChannel
	cancel()
	_ = connection.CloseNow()
	<-errorsChannel
	return firstErr
}

func (c *Client) readLoop(ctx context.Context, connection *websocket.Conn, handler MessageHandler) error {
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			return err
		}
		if messageType != websocket.MessageText {
			continue
		}
		message, err := protocol.Decode(data)
		if err != nil {
			c.logger.Warn("Rejected invalid Relay message", "error", err)
			continue
		}
		handler(ctx, message)
	}
}

func (c *Client) writeLoop(ctx context.Context, connection *websocket.Conn) error {
	ticker := time.NewTicker(c.config.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case message := <-c.outbound:
			if err := c.write(ctx, connection, message); err != nil {
				return err
			}
		case <-ticker.C:
			pingContext, cancel := context.WithTimeout(ctx, c.config.PingInterval/2)
			err := connection.Ping(pingContext)
			cancel()
			if err != nil {
				return fmt.Errorf("Relay ping: %w", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) write(ctx context.Context, connection *websocket.Conn, message protocol.Message) error {
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode Relay message: %w", err)
	}
	if int64(len(data)) > c.config.MaxMessageBytes {
		return fmt.Errorf("Relay message exceeds %d bytes", c.config.MaxMessageBytes)
	}
	if err := connection.Write(ctx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("write Relay message: %w", err)
	}
	return nil
}

func (c *Client) drainOutbound() {
	for {
		select {
		case <-c.outbound:
		default:
			return
		}
	}
}

func (c *Client) backoff(attempt int) time.Duration {
	if attempt > 10 {
		attempt = 10
	}
	delay := c.config.MinBackoff * time.Duration(1<<attempt)
	if delay > c.config.MaxBackoff {
		delay = c.config.MaxBackoff
	}
	if delay <= 1 {
		return delay
	}
	return time.Duration(rand.Int63n(int64(delay)))
}
