package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ai-coding-remote/mac-agent/internal/codexapp"
	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	"github.com/ai-coding-remote/mac-agent/internal/workspace"
)

type fakeCatalog struct{ projects []workspace.Project }

func (f fakeCatalog) List(context.Context) ([]workspace.Project, error) { return f.projects, nil }
func (f fakeCatalog) Resolve(_ context.Context, id string) (workspace.Project, error) {
	for _, project := range f.projects {
		if project.ID == id {
			return project, nil
		}
	}
	return workspace.Project{}, workspace.ErrProjectNotFound
}

type fakeThreads struct {
	values      []codexapp.Thread
	latestCalls *int
}

func (f fakeThreads) ListThreads(_ context.Context, cwd string) ([]codexapp.Thread, error) {
	if cwd == "" {
		return f.values, nil
	}
	var filtered []codexapp.Thread
	for _, thread := range f.values {
		if thread.CWD == cwd {
			filtered = append(filtered, thread)
		}
	}
	return filtered, nil
}

func (f fakeThreads) ListLatestTurns(_ context.Context, threadID string, limit int) ([]codexapp.Turn, error) {
	if f.latestCalls != nil {
		*f.latestCalls++
	}
	for _, thread := range f.values {
		if thread.ID != threadID {
			continue
		}
		turns := make([]codexapp.Turn, 0, min(limit, len(thread.Turns)))
		for index := len(thread.Turns) - 1; index >= 0 && len(turns) < limit; index-- {
			turns = append(turns, thread.Turns[index])
		}
		return turns, nil
	}
	return nil, fmt.Errorf("thread not found: %s", threadID)
}

func (f fakeThreads) ReadThread(_ context.Context, threadID string) (codexapp.Thread, error) {
	for _, thread := range f.values {
		if thread.ID == threadID {
			return thread, nil
		}
	}
	return codexapp.Thread{}, fmt.Errorf("thread not found: %s", threadID)
}
func (f fakeThreads) ListPermissionProfiles(context.Context, string) ([]codexapp.PermissionProfile, error) {
	description := "Write files in the selected workspace"
	return []codexapp.PermissionProfile{{ID: ":workspace", Description: &description, Allowed: true}}, nil
}

func TestServiceListsProjectPermissionProfiles(t *testing.T) {
	service := New(fakeCatalog{[]workspace.Project{{ID: "project-1", Name: "alpha", Path: "/work/alpha"}}}, fakeThreads{})
	snapshot, err := service.ExecutionProfiles(context.Background(), "project-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ProjectID != "project-1" || snapshot.DefaultProfileID != ":workspace" || len(snapshot.Profiles) != 1 || !snapshot.Profiles[0].Allowed {
		t.Fatalf("unexpected profile snapshot: %#v", snapshot)
	}
}

func TestServiceBuildsProjectAndThreadSnapshots(t *testing.T) {
	status, _ := json.Marshal(map[string]any{"type": "idle"})
	source, _ := json.Marshal("cli")
	latestAgent, _ := json.Marshal(map[string]any{"id": "item-agent", "type": "agentMessage", "text": "Latest assistant reply"})
	toolAfterReply, _ := json.Marshal(map[string]any{"id": "item-tool", "type": "commandExecution", "command": "go test ./..."})
	latestCalls := 0
	service := New(fakeCatalog{[]workspace.Project{{ID: "project-1", Name: "alpha", Path: "/work/alpha"}}}, fakeThreads{values: []codexapp.Thread{{
		ID: "thread-1", CWD: "/work/alpha", Preview: "Fix login", Status: status, Source: source, UpdatedAt: 10,
		Turns: []codexapp.Turn{{ID: "turn-1", Items: []json.RawMessage{latestAgent, toolAfterReply}}},
	}}, latestCalls: &latestCalls})
	projects, err := service.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].ThreadCount != 1 || projects[0].UpdatedAt == nil {
		t.Fatalf("unexpected projects: %#v", projects)
	}
	threads, err := service.Threads(context.Background(), "project-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].Title != "Fix login" || threads[0].Status != "idle" || threads[0].LatestMessagePreview != "Latest assistant reply" {
		t.Fatalf("unexpected threads: %#v", threads)
	}
	if _, err := service.Threads(context.Background(), "project-1"); err != nil {
		t.Fatal(err)
	}
	if latestCalls != 1 {
		t.Fatalf("latest turns calls = %d, want cached result after one call", latestCalls)
	}
}

func TestLatestConversationTextSkipsCommentaryAndToolItems(t *testing.T) {
	user, _ := json.Marshal(map[string]any{
		"id": "item-user", "type": "userMessage",
		"content": []map[string]any{{"type": "text", "text": "Latest user request"}},
	})
	commentary, _ := json.Marshal(map[string]any{
		"id": "item-commentary", "type": "agentMessage", "phase": "commentary", "text": "Working on it",
	})
	tool, _ := json.Marshal(map[string]any{
		"id": "item-tool", "type": "commandExecution", "command": "go test ./...",
	})

	preview := latestConversationText([]codexapp.Turn{{
		ID: "latest", Items: []json.RawMessage{user, commentary, tool},
	}})
	if preview != "Latest user request" {
		t.Fatalf("latest conversation preview = %q", preview)
	}
}

func TestServiceUsesDesktopAssignmentBeforeNestedPathFallback(t *testing.T) {
	status, _ := json.Marshal(map[string]any{"type": "notLoaded"})
	source, _ := json.Marshal("appServer")
	projects := []workspace.Project{
		{ID: "parent", Name: "Parent", Path: "/work", RootPaths: []string{"/work"}, ThreadCWDs: map[string]string{"assigned-parent": "/work/child"}},
		{ID: "child", Name: "Child", Path: "/work/child", RootPaths: []string{"/work/child"}, ThreadCWDs: map[string]string{}},
	}
	threads := []codexapp.Thread{
		{ID: "assigned-parent", CWD: "/work/child", Preview: "Assigned", Status: status, Source: source, UpdatedAt: 20},
		{ID: "path-child", CWD: "/work/child", Preview: "Fallback", Status: status, Source: source, UpdatedAt: 10},
	}
	service := New(fakeCatalog{projects}, fakeThreads{values: threads})

	items, err := service.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if items[0].ThreadCount != 1 || items[1].ThreadCount != 1 {
		t.Fatalf("project counts = %#v", items)
	}
	parentThreads, err := service.Threads(context.Background(), "parent")
	if err != nil {
		t.Fatal(err)
	}
	if len(parentThreads) != 1 || parentThreads[0].ID != "assigned-parent" {
		t.Fatalf("parent threads = %#v", parentThreads)
	}
	childThreads, err := service.Threads(context.Background(), "child")
	if err != nil {
		t.Fatal(err)
	}
	if len(childThreads) != 1 || childThreads[0].ID != "path-child" {
		t.Fatalf("child threads = %#v", childThreads)
	}
}

func TestServiceReadsAndNormalizesThreadHistory(t *testing.T) {
	status, _ := json.Marshal(map[string]any{"type": "idle"})
	source, _ := json.Marshal("cli")
	user, _ := json.Marshal(map[string]any{
		"id": "item-user", "type": "userMessage",
		"content": []map[string]any{{"type": "text", "text": "Fix login"}},
	})
	command, _ := json.Marshal(map[string]any{
		"id": "item-command", "type": "commandExecution", "command": "go test ./...", "cwd": "/work/alpha",
		"status": "completed", "aggregatedOutput": "ok", "exitCode": 0,
	})
	fileChange, _ := json.Marshal(map[string]any{
		"id": "item-change", "type": "fileChange", "status": "completed",
		"changes": []map[string]any{{"path": "login.go", "kind": "update", "diff": "@@ fixed @@"}},
	})
	started, completed, duration := int64(10), int64(12), int64(2000)
	thread := codexapp.Thread{
		ID: "thread-1", CWD: "/work/alpha", Preview: "Fix login", Status: status, Source: source,
		CreatedAt: 1, UpdatedAt: 12, Turns: []codexapp.Turn{{
			ID: "turn-1", Status: "completed", StartedAt: &started, CompletedAt: &completed, DurationMS: &duration,
			Items: []json.RawMessage{user, command, fileChange},
		}},
	}
	service := New(fakeCatalog{[]workspace.Project{{ID: "project-1", Name: "alpha", Path: "/work/alpha"}}}, fakeThreads{values: []codexapp.Thread{thread}})

	payload, err := service.ReadThread(context.Background(), "project-1", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if payload.Thread.ID != "thread-1" || len(payload.Thread.Turns) != 1 || len(payload.Thread.Turns[0].Items) != 3 {
		t.Fatalf("unexpected detail: %#v", payload)
	}
	items := payload.Thread.Turns[0].Items
	if items[0].Role != "user" || items[0].Text != "Fix login" || items[1].Command != "go test ./..." || items[1].ExitCode == nil || *items[1].ExitCode != 0 {
		t.Fatalf("unexpected normalized items: %#v", items)
	}
	if len(items[2].Changes) != 1 || items[2].Changes[0].Path != "login.go" {
		t.Fatalf("unexpected file changes: %#v", items[2].Changes)
	}
}

func TestServiceRejectsThreadFromAnotherProject(t *testing.T) {
	status, _ := json.Marshal(map[string]any{"type": "idle"})
	source, _ := json.Marshal("cli")
	projects := []workspace.Project{
		{ID: "alpha", Name: "alpha", Path: "/work/alpha"},
		{ID: "beta", Name: "beta", Path: "/work/beta"},
	}
	service := New(fakeCatalog{projects}, fakeThreads{values: []codexapp.Thread{{
		ID: "thread-beta", CWD: "/work/beta", Preview: "Beta", Status: status, Source: source,
	}}})
	if _, err := service.ReadThread(context.Background(), "alpha", "thread-beta"); err == nil {
		t.Fatal("ReadThread accepted a thread owned by another project")
	}
}

func TestThreadDetailTruncatesOversizedHistory(t *testing.T) {
	large := strings.Repeat("x", maxHistoryFieldBytes+100)
	item := normalizeItem(rawThreadItem{ID: "item-1", Type: "agentMessage", Text: large})
	if !item.Truncated || len(item.Text) > maxHistoryFieldBytes {
		t.Fatalf("item was not truncated: bytes=%d truncated=%v", len(item.Text), item.Truncated)
	}
	command := normalizeItem(rawThreadItem{ID: "item-command", Type: "commandExecution", Command: large})
	if !command.Truncated || len(command.Command) > maxHistoryFieldBytes {
		t.Fatalf("command was not truncated: bytes=%d truncated=%v", len(command.Command), command.Truncated)
	}
	payload := protocol.ThreadDetailPayload{ProjectID: "project", Thread: protocol.ThreadDetail{
		ID: "thread", ProjectID: "project", Turns: []protocol.ThreadHistoryTurn{
			{ID: "old", Items: []protocol.ThreadHistoryItem{{ID: "a", Type: "agentMessage", Text: strings.Repeat("a", 150*1024)}}},
			{ID: "new", Items: []protocol.ThreadHistoryItem{{ID: "b", Type: "agentMessage", Text: strings.Repeat("b", 150*1024)}}},
		},
	}}
	fitThreadDetail(&payload)
	if !payload.Truncated || len(payload.Thread.Turns) != 1 || payload.Thread.Turns[0].ID != "new" || encodedSize(payload) > maxThreadDetailBytes {
		t.Fatalf("unexpected fitted detail: turns=%d bytes=%d truncated=%v", len(payload.Thread.Turns), encodedSize(payload), payload.Truncated)
	}
}
