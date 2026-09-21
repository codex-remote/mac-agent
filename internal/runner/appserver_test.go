package runner

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/codex-remote/mac-agent/internal/codexapp"
)

type fakeAppServer struct {
	handler            func(codexapp.Notification)
	threads            []codexapp.Thread
	profiles           []codexapp.PermissionProfile
	selectedProfileIDs []string
}

func (f *fakeAppServer) SetNotificationHandler(handler func(codexapp.Notification)) {
	f.handler = handler
}
func (f *fakeAppServer) ListThreads(context.Context, string) ([]codexapp.Thread, error) {
	return f.threads, nil
}
func (f *fakeAppServer) ListPermissionProfiles(context.Context, string) ([]codexapp.PermissionProfile, error) {
	return f.profiles, nil
}
func (f *fakeAppServer) StartThread(_ context.Context, _ string, profileID string) (codexapp.Thread, error) {
	f.selectedProfileIDs = append(f.selectedProfileIDs, profileID)
	return codexapp.Thread{ID: "thread-new", CWD: "/repo"}, nil
}
func (f *fakeAppServer) ResumeThread(_ context.Context, id, cwd, profileID string) (codexapp.Thread, error) {
	f.selectedProfileIDs = append(f.selectedProfileIDs, profileID)
	return codexapp.Thread{ID: id, CWD: cwd}, nil
}
func (f *fakeAppServer) StartTurn(_ context.Context, _, _, _, profileID string) (codexapp.Turn, error) {
	f.selectedProfileIDs = append(f.selectedProfileIDs, profileID)
	go func() {
		time.Sleep(5 * time.Millisecond)
		f.handler(notification("item/started", map[string]any{"threadId": "thread-new", "turnId": "turn-1", "item": map[string]any{"id": "item-agent", "type": "agentMessage", "text": ""}}))
		f.handler(notification("item/agentMessage/delta", map[string]any{"threadId": "thread-new", "turnId": "turn-1", "itemId": "item-agent", "delta": "Done"}))
		f.handler(notification("item/completed", map[string]any{"threadId": "thread-new", "turnId": "turn-1", "item": map[string]any{"id": "item-agent", "type": "agentMessage", "phase": "final_answer", "text": "Done"}}))
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
	if len(events) != 5 || events[0].Kind != EventStarted || events[1].Kind != EventItemStarted || events[2].Kind != EventItemDelta || events[3].Stream != "assistant" || events[4].Kind != EventItemCompleted {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestAppServerValidatesAndAppliesPermissionProfile(t *testing.T) {
	client := &fakeAppServer{profiles: []codexapp.PermissionProfile{{ID: ":danger-full-access", Allowed: true}}}
	request := Request{ProjectID: "project-1", Prompt: "fix", WorkingDir: "/repo", PermissionProfileID: ":danger-full-access"}
	_, err := (AppServer{Client: client}).Run(context.Background(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(client.selectedProfileIDs) != 2 || client.selectedProfileIDs[0] != ":danger-full-access" || client.selectedProfileIDs[1] != ":danger-full-access" {
		t.Fatalf("selected profiles = %#v", client.selectedProfileIDs)
	}
}

func TestAppServerRejectsUnavailablePermissionProfile(t *testing.T) {
	client := &fakeAppServer{profiles: []codexapp.PermissionProfile{{ID: ":workspace", Allowed: true}}}
	request := Request{ProjectID: "project-1", Prompt: "fix", WorkingDir: "/repo", PermissionProfileID: ":forged"}
	if _, err := (AppServer{Client: client}).Run(context.Background(), request, nil); err == nil {
		t.Fatal("expected unavailable permission profile to be rejected")
	}
}

func TestTranslateNotificationNormalizesToolLifecycleAndProgress(t *testing.T) {
	started, ok := translateNotification(notification("item/started", map[string]any{
		"threadId": "thread-1", "turnId": "turn-1",
		"item": map[string]any{"id": "item-tool", "type": "mcpToolCall", "server": "github", "tool": "search", "status": "inProgress"},
	}))
	if !ok || started.kind != EventItemStarted || started.item.Name != "github/search" {
		t.Fatalf("unexpected started event: %#v", started)
	}
	progress, ok := translateNotification(notification("item/mcpToolCall/progress", map[string]any{
		"threadId": "thread-1", "turnId": "turn-1", "itemId": "item-tool", "message": "Searching repositories",
	}))
	if !ok || progress.kind != EventItemDelta || progress.itemID != "item-tool" || progress.text != "Searching repositories" {
		t.Fatalf("unexpected progress event: %#v", progress)
	}
}

func notification(method string, params any) codexapp.Notification {
	data, _ := json.Marshal(params)
	return codexapp.Notification{Method: method, Params: data}
}
