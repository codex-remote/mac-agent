package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
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
	thread, err := client.ResumeThread(ctx, "thread-1", "/workspace/alpha")
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
