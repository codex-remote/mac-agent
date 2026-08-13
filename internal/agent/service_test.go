package agent

import (
	"context"
	"testing"

	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	turncontrol "github.com/ai-coding-remote/mac-agent/internal/turn"
)

type serviceController struct {
	started  bool
	snapshot turncontrol.Snapshot
}

func (c *serviceController) Start(_ context.Context, _ string, _ protocol.TurnStartPayload, _ turncontrol.EventSink) error {
	c.started = true
	return nil
}
func (c *serviceController) Interrupt(string, string) error { return nil }
func (c *serviceController) Snapshot() turncontrol.Snapshot { return c.snapshot }

type serviceInventory struct{}

var testCapabilities = protocol.AgentCapabilitiesPayload{
	Restricted: true, SandboxMode: "workspace-write", ApprovalPolicy: "never", WritableScope: "selected_project",
}

func (serviceInventory) Projects(context.Context) ([]protocol.Project, error) {
	return []protocol.Project{{ID: "project-1", Name: "alpha", Path: "/work/alpha"}}, nil
}
func (serviceInventory) Threads(context.Context, string) ([]protocol.Thread, error) {
	return []protocol.Thread{{ID: "thread-1", ProjectID: "project-1", Title: "Fix tests"}}, nil
}
func (serviceInventory) ReadThread(context.Context, string, string) (protocol.ThreadDetailPayload, error) {
	return protocol.ThreadDetailPayload{ProjectID: "project-1", Thread: protocol.ThreadDetail{ID: "thread-1", ProjectID: "project-1"}}, nil
}
func (serviceInventory) ExecutionProfiles(context.Context, string) (protocol.ExecutionProfileSnapshotPayload, error) {
	return protocol.ExecutionProfileSnapshotPayload{
		ProjectID: "project-1", DefaultProfileID: ":workspace",
		Profiles: []protocol.ExecutionProfile{{ID: ":workspace", Allowed: true}},
	}, nil
}

func TestServiceHandlesInventoryQueriesAndTurnStart(t *testing.T) {
	controller := &serviceController{snapshot: turncontrol.Snapshot{Status: protocol.StatusIdle}}
	var published []protocol.Message
	service := NewService(context.Background(), "mac", "test", protocol.Sender{Kind: "device", ID: "mac"}, testCapabilities, controller, serviceInventory{}, func(message protocol.Message) error {
		published = append(published, message)
		return nil
	})
	list, _ := protocol.NewMessage(protocol.TypeProjectList, "projects", protocol.Sender{Kind: "user", ID: "local"}, protocol.ProjectListPayload{})
	service.Handle(context.Background(), list)
	if len(published) != 1 || published[0].Type != protocol.TypeProjectSnapshot {
		t.Fatalf("unexpected project response: %#v", published)
	}
	profiles, _ := protocol.NewMessage(protocol.TypeExecutionProfileList, "profiles", protocol.Sender{Kind: "user", ID: "local"}, protocol.ExecutionProfileListPayload{ProjectID: "project-1"})
	service.Handle(context.Background(), profiles)
	if len(published) != 2 || published[1].Type != protocol.TypeExecutionProfileSnapshot {
		t.Fatalf("unexpected execution profile response: %#v", published)
	}
	threads, _ := protocol.NewMessage(protocol.TypeThreadList, "threads", protocol.Sender{Kind: "user", ID: "local"}, protocol.ThreadListPayload{ProjectID: "project-1"})
	service.Handle(context.Background(), threads)
	if len(published) != 3 || published[2].Type != protocol.TypeThreadSnapshot || published[2].TraceID != "threads" {
		t.Fatalf("unexpected thread response: %#v", published)
	}
	threadPayload, err := protocol.PayloadAs[protocol.ThreadSnapshotPayload](published[2])
	if err != nil {
		t.Fatal(err)
	}
	if threadPayload.ProjectID != "project-1" || len(threadPayload.Threads) != 1 || threadPayload.Threads[0].ID != "thread-1" {
		t.Fatalf("unexpected thread payload: %#v", threadPayload)
	}
	read, _ := protocol.NewMessage(protocol.TypeThreadRead, "thread-read", protocol.Sender{Kind: "user", ID: "local"}, protocol.ThreadReadPayload{ProjectID: "project-1", ThreadID: "thread-1"})
	service.Handle(context.Background(), read)
	if len(published) != 4 || published[3].Type != protocol.TypeThreadDetail || published[3].TraceID != "thread-read" {
		t.Fatalf("unexpected thread detail response: %#v", published)
	}
	start, _ := protocol.NewMessage(protocol.TypeTurnStart, "turn-request", protocol.Sender{Kind: "user", ID: "local"}, protocol.TurnStartPayload{ProjectID: "project-1", Prompt: "fix"})
	service.Handle(context.Background(), start)
	if !controller.started {
		t.Fatal("controller was not started")
	}
}

func TestServiceInitialMessagesIncludeTurnSnapshot(t *testing.T) {
	controller := &serviceController{snapshot: turncontrol.Snapshot{
		TraceID: "trace-1", ProjectID: "project-1", ThreadID: "thread-1", TurnID: "turn-1", Status: protocol.StatusRunning, RecentOutput: []string{"line"},
	}}
	service := NewService(context.Background(), "mac", "test", protocol.Sender{Kind: "device", ID: "mac"}, testCapabilities, controller, serviceInventory{}, func(protocol.Message) error { return nil })
	messages := service.InitialMessages()
	if len(messages) != 4 || messages[2].Type != protocol.TypeAgentCapabilities || messages[3].Type != protocol.TypeTurnSnapshot {
		t.Fatalf("unexpected initial messages: %#v", messages)
	}
}

func TestServiceRejectionIncludesExecutionContext(t *testing.T) {
	controller := &serviceController{snapshot: turncontrol.Snapshot{Status: protocol.StatusIdle}}
	var published []protocol.Message
	service := NewService(context.Background(), "mac", "test", protocol.Sender{Kind: "device", ID: "mac"}, testCapabilities, controller, serviceInventory{}, func(message protocol.Message) error {
		published = append(published, message)
		return nil
	})
	invalid, _ := protocol.NewMessage("unsupported", "trace-1", protocol.Sender{Kind: "user", ID: "local"}, struct{}{})
	service.Handle(context.Background(), invalid)
	if len(published) != 1 || published[0].Type != protocol.TypeTurnRejected {
		t.Fatalf("unexpected response: %#v", published)
	}
	payload, err := protocol.PayloadAs[protocol.TurnRejectedPayload](published[0])
	if err != nil {
		t.Fatal(err)
	}
	if payload.ExecutionContext == nil || payload.ExecutionContext.SandboxMode != "workspace-write" || !payload.ExecutionContext.Restricted {
		t.Fatalf("missing execution context: %#v", payload)
	}
}

func TestServiceAcceptsTerminalTurnAcknowledgement(t *testing.T) {
	controller := &serviceController{snapshot: turncontrol.Snapshot{Status: protocol.StatusIdle}}
	var published []protocol.Message
	service := NewService(context.Background(), "mac", "test", protocol.Sender{Kind: "device", ID: "mac"}, testCapabilities, controller, serviceInventory{}, func(message protocol.Message) error {
		published = append(published, message)
		return nil
	})
	acknowledged, _ := protocol.NewMessage(
		protocol.TypeTurnAcknowledged,
		"trace-1",
		protocol.Sender{Kind: "user", ID: "iphone"},
		protocol.TurnAcknowledgedPayload{TurnID: "turn-1", Status: "completed"},
	)
	service.Handle(context.Background(), acknowledged)
	if len(published) != 0 {
		t.Fatalf("acknowledgement produced a response: %#v", published)
	}
}
