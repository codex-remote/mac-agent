package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const maxRPCMessageBytes = 64 * 1024 * 1024

const (
	RemoteSandboxMode    = "workspace-write"
	RemoteApprovalPolicy = "never"
)

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("Codex app-server error %d: %s", e.Code, e.Message)
}

type Notification struct {
	Method string
	Params json.RawMessage
}

type Thread struct {
	ID        string          `json:"id"`
	Name      *string         `json:"name"`
	CWD       string          `json:"cwd"`
	Preview   string          `json:"preview"`
	Status    json.RawMessage `json:"status"`
	Source    json.RawMessage `json:"source"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
	Turns     []Turn          `json:"turns"`
}

func (t Thread) Title() string {
	if t.Name != nil && *t.Name != "" {
		return *t.Name
	}
	if t.Preview != "" {
		const maxTitleRunes = 80
		characters := []rune(t.Preview)
		if len(characters) > maxTitleRunes {
			return string(characters[:maxTitleRunes]) + "..."
		}
		return t.Preview
	}
	return "Untitled session"
}

func (t Thread) StatusName() string {
	var value struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(t.Status, &value) == nil && value.Type != "" {
		return value.Type
	}
	return "unknown"
}

func (t Thread) SourceName() string {
	var name string
	if json.Unmarshal(t.Source, &name) == nil && name != "" {
		return name
	}
	var custom struct {
		Custom string `json:"custom"`
	}
	if json.Unmarshal(t.Source, &custom) == nil && custom.Custom != "" {
		return custom.Custom
	}
	return "unknown"
}

type Turn struct {
	ID          string            `json:"id"`
	Status      string            `json:"status"`
	StartedAt   *int64            `json:"startedAt"`
	CompletedAt *int64            `json:"completedAt"`
	DurationMS  *int64            `json:"durationMs"`
	Items       []json.RawMessage `json:"items"`
	Error       *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type PermissionProfile struct {
	ID          string  `json:"id"`
	Description *string `json:"description"`
	Allowed     bool    `json:"allowed"`
}

type rpcMessage struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type Client struct {
	reader io.Reader
	writer io.Writer
	closer io.Closer

	nextID       atomic.Int64
	writeMu      sync.Mutex
	mu           sync.Mutex
	pending      map[int64]chan rpcMessage
	handler      func(Notification)
	closed       chan struct{}
	resourceOnce sync.Once
	finishOnce   sync.Once
	readErr      error
}

func NewClient(reader io.Reader, writer io.Writer, closer io.Closer) *Client {
	client := &Client{
		reader:  reader,
		writer:  writer,
		closer:  closer,
		pending: make(map[int64]chan rpcMessage),
		closed:  make(chan struct{}),
	}
	go client.readLoop()
	return client
}

func (c *Client) Initialize(ctx context.Context, name, version string) error {
	params := map[string]any{
		"clientInfo":   map[string]any{"name": name, "title": "AI Coding Remote Mac Agent", "version": version},
		"capabilities": map[string]any{"experimentalApi": true},
	}
	var response json.RawMessage
	if err := c.Call(ctx, "initialize", params, &response); err != nil {
		return fmt.Errorf("initialize Codex app-server: %w", err)
	}
	if err := c.Notify("initialized", map[string]any{}); err != nil {
		return fmt.Errorf("notify Codex app-server initialized: %w", err)
	}
	return nil
}

func (c *Client) ListLatestTurns(ctx context.Context, threadID string, limit int) ([]Turn, error) {
	if limit < 1 {
		limit = 1
	}
	var response struct {
		Data []Turn `json:"data"`
	}
	params := map[string]any{
		"threadId":      threadID,
		"limit":         limit,
		"sortDirection": "desc",
		"itemsView":     "summary",
	}
	if err := c.Call(ctx, "thread/turns/list", params, &response); err != nil {
		return nil, fmt.Errorf("list latest Codex thread turns: %w", err)
	}
	return response.Data, nil
}

func (c *Client) SetNotificationHandler(handler func(Notification)) {
	c.mu.Lock()
	c.handler = handler
	c.mu.Unlock()
}

func (c *Client) ListThreads(ctx context.Context, cwd string) ([]Thread, error) {
	var threads []Thread
	var cursor *string
	for page := 0; page < 100; page++ {
		params := map[string]any{
			"limit":         100,
			"sortKey":       "updated_at",
			"sortDirection": "desc",
			"sourceKinds":   []string{"cli", "vscode", "exec", "appServer"},
		}
		if cwd != "" {
			params["cwd"] = cwd
		}
		if cursor != nil {
			params["cursor"] = *cursor
		}
		var response struct {
			Data       []Thread `json:"data"`
			NextCursor *string  `json:"nextCursor"`
		}
		if err := c.Call(ctx, "thread/list", params, &response); err != nil {
			return nil, fmt.Errorf("list Codex threads: %w", err)
		}
		threads = append(threads, response.Data...)
		if response.NextCursor == nil || *response.NextCursor == "" {
			break
		}
		cursor = response.NextCursor
	}
	return threads, nil
}

func (c *Client) ReadThread(ctx context.Context, threadID string) (Thread, error) {
	var response struct {
		Thread Thread `json:"thread"`
	}
	params := map[string]any{"threadId": threadID, "includeTurns": true}
	if err := c.Call(ctx, "thread/read", params, &response); err != nil {
		return Thread{}, fmt.Errorf("read Codex thread: %w", err)
	}
	return response.Thread, nil
}

func (c *Client) ListPermissionProfiles(ctx context.Context, cwd string) ([]PermissionProfile, error) {
	var profiles []PermissionProfile
	var cursor *string
	for page := 0; page < 100; page++ {
		params := map[string]any{"cwd": cwd, "limit": 100}
		if cursor != nil {
			params["cursor"] = *cursor
		}
		var response struct {
			Data       []PermissionProfile `json:"data"`
			NextCursor *string             `json:"nextCursor"`
		}
		if err := c.Call(ctx, "permissionProfile/list", params, &response); err != nil {
			return nil, fmt.Errorf("list Codex permission profiles: %w", err)
		}
		profiles = append(profiles, response.Data...)
		if response.NextCursor == nil || *response.NextCursor == "" {
			break
		}
		cursor = response.NextCursor
	}
	return profiles, nil
}

func (c *Client) StartThread(ctx context.Context, cwd, permissionProfileID string) (Thread, error) {
	var response struct {
		Thread Thread `json:"thread"`
	}
	params := threadExecutionParams(cwd, permissionProfileID)
	params["ephemeral"] = false
	if err := c.Call(ctx, "thread/start", params, &response); err != nil {
		return Thread{}, fmt.Errorf("start Codex thread: %w", err)
	}
	return response.Thread, nil
}

func (c *Client) ResumeThread(ctx context.Context, threadID, cwd, permissionProfileID string) (Thread, error) {
	var response struct {
		Thread Thread `json:"thread"`
	}
	params := threadExecutionParams(cwd, permissionProfileID)
	params["threadId"] = threadID
	if err := c.Call(ctx, "thread/resume", params, &response); err != nil {
		return Thread{}, fmt.Errorf("resume Codex thread: %w", err)
	}
	return response.Thread, nil
}

func (c *Client) StartTurn(ctx context.Context, threadID, cwd, prompt, permissionProfileID string) (Turn, error) {
	var response struct {
		Turn Turn `json:"turn"`
	}
	params := map[string]any{
		"threadId": threadID,
		"cwd":      cwd,
		"input":    []map[string]any{{"type": "text", "text": prompt}},
	}
	if permissionProfileID == "" {
		params["approvalPolicy"] = RemoteApprovalPolicy
	} else {
		params["permissions"] = permissionProfileID
	}
	if err := c.Call(ctx, "turn/start", params, &response); err != nil {
		return Turn{}, fmt.Errorf("start Codex turn: %w", err)
	}
	return response.Turn, nil
}

func threadExecutionParams(cwd, permissionProfileID string) map[string]any {
	params := map[string]any{"cwd": cwd}
	if permissionProfileID == "" {
		params["sandbox"] = RemoteSandboxMode
		params["approvalPolicy"] = RemoteApprovalPolicy
	} else {
		params["permissions"] = permissionProfileID
	}
	return params
}

func (c *Client) InterruptTurn(ctx context.Context, threadID, turnID string) error {
	var response json.RawMessage
	if err := c.Call(ctx, "turn/interrupt", map[string]any{"threadId": threadID, "turnId": turnID}, &response); err != nil {
		return fmt.Errorf("interrupt Codex turn: %w", err)
	}
	return nil
}

func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	id := c.nextID.Add(1)
	responseChannel := make(chan rpcMessage, 1)
	c.mu.Lock()
	if c.readErr != nil {
		err := c.readErr
		c.mu.Unlock()
		return err
	}
	c.pending[id] = responseChannel
	c.mu.Unlock()

	request := map[string]any{"id": id, "method": method, "params": params}
	if err := c.write(request); err != nil {
		c.removePending(id)
		return err
	}
	select {
	case response := <-responseChannel:
		if response.Error != nil {
			return response.Error
		}
		if result == nil || len(response.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode %s response: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	case <-c.closed:
		c.mu.Lock()
		err := c.readErr
		c.mu.Unlock()
		if err == nil {
			err = io.EOF
		}
		return err
	}
}

func (c *Client) Notify(method string, params any) error {
	return c.write(map[string]any{"method": method, "params": params})
}

func (c *Client) Close() error {
	c.resourceOnce.Do(func() {
		if c.closer != nil {
			_ = c.closer.Close()
		}
	})
	c.finish(io.EOF)
	return nil
}

func (c *Client) readLoop() {
	scanner := bufio.NewScanner(c.reader)
	scanner.Buffer(make([]byte, 64*1024), maxRPCMessageBytes)
	for scanner.Scan() {
		var message rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			c.finish(fmt.Errorf("decode Codex app-server message: %w", err))
			return
		}
		if message.ID != nil && message.Method == "" {
			c.mu.Lock()
			channel := c.pending[*message.ID]
			delete(c.pending, *message.ID)
			c.mu.Unlock()
			if channel != nil {
				channel <- message
			}
			continue
		}
		if message.ID != nil && message.Method != "" {
			_ = c.write(map[string]any{
				"id":    *message.ID,
				"error": map[string]any{"code": -32601, "message": "Mac Agent does not support interactive app-server requests in MVP"},
			})
			continue
		}
		if message.Method != "" {
			c.mu.Lock()
			handler := c.handler
			c.mu.Unlock()
			if handler != nil {
				handler(Notification{Method: message.Method, Params: message.Params})
			}
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	c.finish(fmt.Errorf("Codex app-server stopped: %w", err))
}

func (c *Client) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode Codex app-server message: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.writer.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write Codex app-server message: %w", err)
	}
	return nil
}

func (c *Client) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) finish(err error) {
	c.finishOnce.Do(func() {
		c.mu.Lock()
		c.readErr = err
		c.pending = make(map[int64]chan rpcMessage)
		c.mu.Unlock()
		close(c.closed)
	})
}

func UnixTime(seconds int64) time.Time {
	return time.Unix(seconds, 0).UTC()
}

func IsClosed(err error) bool {
	return errors.Is(err, io.EOF)
}
