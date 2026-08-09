package runner

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ai-coding-remote/mac-agent/internal/codexapp"
)

type fakeAppServer struct {
	handler func(codexapp.Notification)
	threads []codexapp.Thread
}

func (f *fakeAppServer) SetNotificationHandler(handler func(codexapp.Notification)) {
	f.handler = handler
}
func (f *fakeAppServer) ListThreads(context.Context, string) ([]codexapp.Thread, error) {
	return f.threads, nil
}
func (f *fakeAppServer) StartThread(context.Context, string) (codexapp.Thread, error) {
	return codexapp.Thread{ID: "thread-new", CWD: "/repo"}, nil
}
func (f *fakeAppServer) ResumeThread(_ context.Context, id, cwd string) (codexapp.Thread, error) {
	return codexapp.Thread{ID: id, CWD: cwd}, nil
}
func (f *fakeAppServer) StartTurn(context.Context, string, string, string) (codexapp.Turn, error) {
	go func() {
		time.Sleep(5 * time.Millisecond)
		f.handler(notification("item/agentMessage/delta", map[string]any{"threadId": "thread-new", "turnId": "turn-1", "delta": "Done"}))
		f.handler(notification("turn/completed", map[string]any{"threadId": "thread-new", "turn": map[string]any{"id": "turn-1", "status": "completed", "durationMs": 12}}))
	}()
	return codexapp.Turn{ID: "turn-1", Status: "inProgress"}, nil
}
func (f *fakeAppServer) InterruptTurn(context.Context, string, string) error { return nil }

func TestAppServerRunsNewPersistentThread(t *testing.T) {
	client := &fakeAppServer{}
	var events []Event
	result, err := (AppServer{Client: client}).Run(context.Background(), Request{ProjectID: "project-1", Prompt: "fix", WorkingDir: "/repo"}, func(event Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ThreadID != "thread-new" || result.TurnID != "turn-1" || result.Summary != "Done" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(events) != 2 || events[0].Kind != EventStarted || events[1].Stream != "assistant" {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func notification(method string, params any) codexapp.Notification {
	data, _ := json.Marshal(params)
	return codexapp.Notification{Method: method, Params: data}
}
