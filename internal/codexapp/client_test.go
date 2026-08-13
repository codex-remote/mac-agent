package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestClientInitializesListsThreadsAndReceivesNotifications(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := NewClient(clientConnection, clientConnection, clientConnection)
	defer client.Close()
	defer serverConnection.Close()

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- serveFakeAppServer(serverConnection)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Initialize(ctx, "test-client", "test"); err != nil {
		t.Fatal(err)
	}
	notification := make(chan Notification, 1)
	client.SetNotificationHandler(func(value Notification) { notification <- value })
	threads, err := client.ListThreads(ctx, "/workspace/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ID != "thread-1" || threads[0].Title() != "Fix tests" {
		t.Fatalf("unexpected threads: %#v", threads)
	}
	select {
	case event := <-notification:
		if event.Method != "agent/status/updated" {
			t.Fatalf("notification method = %q", event.Method)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for notification")
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestResumeThreadUsesStableAPIParameters(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := NewClient(clientConnection, clientConnection, clientConnection)
	defer client.Close()
	defer serverConnection.Close()

	serverErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(serverConnection)
		if !scanner.Scan() {
			serverErrors <- scanner.Err()
			return
		}
		var request rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			serverErrors <- err
			return
		}
		var params map[string]any
		if err := json.Unmarshal(request.Params, &params); err != nil {
			serverErrors <- err
			return
		}
		if _, exists := params["excludeTurns"]; exists {
			serverErrors <- fmt.Errorf("thread/resume must not use experimental excludeTurns")
			return
		}
		if params["permissions"] != ":danger-full-access" {
			serverErrors <- fmt.Errorf("permissions = %#v", params["permissions"])
			return
		}
		if _, exists := params["sandbox"]; exists {
			serverErrors <- fmt.Errorf("permissions must not be combined with sandbox")
			return
		}
		encoder := json.NewEncoder(serverConnection)
		serverErrors <- encoder.Encode(map[string]any{
			"id": *request.ID,
			"result": map[string]any{"thread": map[string]any{
				"id": "thread-1", "cwd": "/workspace/alpha", "preview": "continued",
				"status": map[string]any{"type": "idle"}, "source": "appServer", "createdAt": 1, "updatedAt": 2,
			}},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	thread, err := client.ResumeThread(ctx, "thread-1", "/workspace/alpha", ":danger-full-access")
	if err != nil {
		t.Fatal(err)
	}
	if thread.ID != "thread-1" {
		t.Fatalf("thread ID = %q", thread.ID)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestStartTurnUsesPermissionProfileWithoutLegacyExecutionParameters(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := NewClient(clientConnection, clientConnection, clientConnection)
	defer client.Close()
	defer serverConnection.Close()

	serverErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(serverConnection)
		if !scanner.Scan() {
			serverErrors <- scanner.Err()
			return
		}
		var request rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			serverErrors <- err
			return
		}
		var params map[string]any
		if err := json.Unmarshal(request.Params, &params); err != nil {
			serverErrors <- err
			return
		}
		if params["permissions"] != ":danger-full-access" {
			serverErrors <- fmt.Errorf("permissions = %#v", params["permissions"])
			return
		}
		for _, forbidden := range []string{"sandboxPolicy", "approvalPolicy"} {
			if _, exists := params[forbidden]; exists {
				serverErrors <- fmt.Errorf("permissions must not be combined with %s", forbidden)
				return
			}
		}
		serverErrors <- json.NewEncoder(serverConnection).Encode(map[string]any{
			"id": *request.ID, "result": map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	turn, err := client.StartTurn(ctx, "thread-1", "/workspace/alpha", "fix", ":danger-full-access")
	if err != nil {
		t.Fatal(err)
	}
	if turn.ID != "turn-1" {
		t.Fatalf("turn ID = %q", turn.ID)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestListPermissionProfilesUsesProjectCWD(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := NewClient(clientConnection, clientConnection, clientConnection)
	defer client.Close()
	defer serverConnection.Close()

	serverErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(serverConnection)
		if !scanner.Scan() {
			serverErrors <- scanner.Err()
			return
		}
		var request rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			serverErrors <- err
			return
		}
		var params map[string]any
		if err := json.Unmarshal(request.Params, &params); err != nil {
			serverErrors <- err
			return
		}
		if params["cwd"] != "/workspace/alpha" {
			serverErrors <- fmt.Errorf("cwd = %#v", params["cwd"])
			return
		}
		serverErrors <- json.NewEncoder(serverConnection).Encode(map[string]any{
			"id":     *request.ID,
			"result": map[string]any{"data": []map[string]any{{"id": ":workspace", "allowed": true}}, "nextCursor": nil},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	profiles, err := client.ListPermissionProfiles(ctx, "/workspace/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].ID != ":workspace" || !profiles[0].Allowed {
		t.Fatalf("profiles = %#v", profiles)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestReadThreadIncludesTurns(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := NewClient(clientConnection, clientConnection, clientConnection)
	defer client.Close()
	defer serverConnection.Close()

	serverErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(serverConnection)
		if !scanner.Scan() {
			serverErrors <- scanner.Err()
			return
		}
		var request rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			serverErrors <- err
			return
		}
		var params map[string]any
		if err := json.Unmarshal(request.Params, &params); err != nil {
			serverErrors <- err
			return
		}
		if request.Method != "thread/read" || params["threadId"] != "thread-1" || params["includeTurns"] != true {
			serverErrors <- fmt.Errorf("unexpected thread/read request: method=%s params=%v", request.Method, params)
			return
		}
		serverErrors <- json.NewEncoder(serverConnection).Encode(map[string]any{
			"id": *request.ID,
			"result": map[string]any{"thread": map[string]any{
				"id": "thread-1", "cwd": "/workspace/alpha", "preview": "fix",
				"status": map[string]any{"type": "idle"}, "source": "cli", "createdAt": 1, "updatedAt": 2,
				"turns": []map[string]any{{"id": "turn-1", "status": "completed", "items": []map[string]any{{"id": "item-1", "type": "agentMessage", "text": "done"}}}},
			}},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	thread, err := client.ReadThread(ctx, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Turns) != 1 || len(thread.Turns[0].Items) != 1 {
		t.Fatalf("thread history = %#v", thread.Turns)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestListLatestTurnsUsesBoundedSummaryPage(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := NewClient(clientConnection, clientConnection, clientConnection)
	defer client.Close()
	defer serverConnection.Close()

	serverErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(serverConnection)
		if !scanner.Scan() {
			serverErrors <- scanner.Err()
			return
		}
		var request rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			serverErrors <- err
			return
		}
		var params map[string]any
		if err := json.Unmarshal(request.Params, &params); err != nil {
			serverErrors <- err
			return
		}
		if request.Method != "thread/turns/list" || params["threadId"] != "thread-1" || params["limit"] != float64(3) || params["sortDirection"] != "desc" || params["itemsView"] != "summary" {
			serverErrors <- fmt.Errorf("unexpected thread/turns/list request: method=%s params=%v", request.Method, params)
			return
		}
		serverErrors <- json.NewEncoder(serverConnection).Encode(map[string]any{
			"id": *request.ID,
			"result": map[string]any{"data": []map[string]any{{
				"id": "turn-latest", "status": "completed", "itemsView": "summary",
				"items": []map[string]any{{"id": "item-agent", "type": "agentMessage", "text": "latest response"}},
			}}},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	turns, err := client.ListLatestTurns(ctx, "thread-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].ID != "turn-latest" || len(turns[0].Items) != 1 {
		t.Fatalf("latest turns = %#v", turns)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestClientAcceptsLargeCodexAppServerResponse(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := NewClient(clientConnection, clientConnection, clientConnection)
	defer client.Close()
	defer serverConnection.Close()

	serverErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(serverConnection)
		if !scanner.Scan() {
			serverErrors <- scanner.Err()
			return
		}
		var request rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			serverErrors <- err
			return
		}
		largePreview := strings.Repeat("x", 5*1024*1024)
		serverErrors <- json.NewEncoder(serverConnection).Encode(map[string]any{
			"id": *request.ID,
			"result": map[string]any{
				"data": []map[string]any{{
					"id": "thread-large", "name": "Large response", "cwd": "/workspace/alpha", "preview": largePreview,
					"status": map[string]any{"type": "idle"}, "source": "cli", "createdAt": 1, "updatedAt": 2,
				}},
				"nextCursor": nil,
			},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	threads, err := client.ListThreads(ctx, "/workspace/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ID != "thread-large" || len(threads[0].Preview) != 5*1024*1024 {
		t.Fatalf("unexpected large response: count=%d preview=%d", len(threads), len(threads[0].Preview))
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func serveFakeAppServer(connection net.Conn) error {
	scanner := bufio.NewScanner(connection)
	encoder := json.NewEncoder(connection)
	if !scanner.Scan() {
		return scanner.Err()
	}
	var initialize rpcMessage
	if err := json.Unmarshal(scanner.Bytes(), &initialize); err != nil {
		return err
	}
	var initializeParams struct {
		Capabilities struct {
			ExperimentalAPI bool `json:"experimentalApi"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(initialize.Params, &initializeParams); err != nil {
		return err
	}
	if !initializeParams.Capabilities.ExperimentalAPI {
		return fmt.Errorf("experimentalApi capability must be enabled for thread/turns/list")
	}
	if err := encoder.Encode(map[string]any{"id": *initialize.ID, "result": map[string]any{}}); err != nil {
		return err
	}
	if !scanner.Scan() {
		return scanner.Err()
	}
	var initialized rpcMessage
	if err := json.Unmarshal(scanner.Bytes(), &initialized); err != nil {
		return err
	}
	if initialized.Method != "initialized" {
		return &RPCError{Code: -1, Message: "expected initialized notification"}
	}
	if !scanner.Scan() {
		return scanner.Err()
	}
	var list rpcMessage
	if err := json.Unmarshal(scanner.Bytes(), &list); err != nil {
		return err
	}
	if err := encoder.Encode(map[string]any{
		"method": "agent/status/updated",
		"params": map[string]any{"status": "ready"},
	}); err != nil {
		return err
	}
	return encoder.Encode(map[string]any{
		"id": *list.ID,
		"result": map[string]any{
			"data": []map[string]any{{
				"id": "thread-1", "name": "Fix tests", "cwd": "/workspace/alpha", "preview": "fix",
				"status": map[string]any{"type": "idle"}, "source": "cli", "createdAt": 1, "updatedAt": 2,
			}},
			"nextCursor": nil,
		},
	})
}
