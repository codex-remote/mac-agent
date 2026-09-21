package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/codex-remote/mac-agent/internal/protocol"
)

func TestClientExchangesMessages(t *testing.T) {
	serverError := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			serverError <- err
			return
		}
		defer connection.CloseNow()
		_, data, err := connection.Read(request.Context())
		if err != nil {
			serverError <- err
			return
		}
		hello, err := protocol.Decode(data)
		if err != nil || hello.Type != protocol.TypeAgentHello {
			serverError <- fmt.Errorf("unexpected hello %q: %v", hello.Type, err)
			return
		}
		start, err := protocol.NewMessage(protocol.TypeTurnStart, "trace-1", protocol.Sender{Kind: "user", ID: "local"}, protocol.TurnStartPayload{ProjectID: "project-1", Prompt: "test"})
		if err != nil {
			serverError <- err
			return
		}
		if err := writeServerMessage(request.Context(), connection, start); err != nil {
			serverError <- err
			return
		}
		_, data, err = connection.Read(request.Context())
		if err != nil {
			serverError <- err
			return
		}
		responseMessage, err := protocol.Decode(data)
		if err != nil || responseMessage.Type != protocol.TypeTurnRejected {
			serverError <- fmt.Errorf("unexpected response %q: %v", responseMessage.Type, err)
			return
		}
		serverError <- nil
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := New(Config{
		URL: strings.Replace(server.URL, "http://", "ws://", 1), MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, nil)
	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx, func() []protocol.Message {
			hello, _ := protocol.NewMessage(protocol.TypeAgentHello, "agent", protocol.Sender{Kind: "device", ID: "mac"}, protocol.AgentHelloPayload{Name: "mac", Version: "test", Status: protocol.StatusIdle})
			return []protocol.Message{hello}
		}, func(_ context.Context, message protocol.Message) {
			rejected, _ := protocol.NewMessage(protocol.TypeTurnRejected, message.TraceID, protocol.Sender{Kind: "device", ID: "mac"}, protocol.TurnRejectedPayload{Code: "TEST"})
			_ = client.Publish(rejected)
		})
	}()
	select {
	case err := <-serverError:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for exchange")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("client did not stop")
	}
}

func TestClientDefaultMessageLimitAccommodatesDiffResult(t *testing.T) {
	client := New(Config{}, nil)
	if client.config.MaxMessageBytes != 256*1024 {
		t.Fatalf("MaxMessageBytes = %d", client.config.MaxMessageBytes)
	}
}

func writeServerMessage(ctx context.Context, connection *websocket.Conn, message protocol.Message) error {
	data, err := jsonMarshal(message)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, data)
}

func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}
