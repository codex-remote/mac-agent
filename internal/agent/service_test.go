package agent

import (
	"context"
	"testing"
	"time"

	"github.com/codex-remote/mac-agent/internal/protocol"
	turncontrol "github.com/codex-remote/mac-agent/internal/turn"
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
	SupportsSourceRead: true,
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

type blockedBootstrapInventory struct {
	serviceInventory
}

func (blockedBootstrapInventory) ReadThread(ctx context.Context, _, _ string) (protocol.ThreadDetailPayload, error) {
	<-ctx.Done()
	return protocol.ThreadDetailPayload{}, ctx.Err()
}

type progressBootstrapInventory struct {
	serviceInventory
}

type serviceSourceReader struct {
	snapshot protocol.SourceSnapshotPayload
	err      error
}

func (r serviceSourceReader) Read(context.Context, protocol.SourceReadPayload) (protocol.SourceSnapshotPayload, error) {
	return r.snapshot, r.err
}

func (progressBootstrapInventory) Projects(context.Context) ([]protocol.Project, error) {
	return []protocol.Project{{ID: "project-1"}, {ID: "project-2"}}, nil
}

func (progressBootstrapInventory) Threads(_ context.Context, projectID string) ([]protocol.Thread, error) {
	if projectID == "project-1" {
		return []protocol.Thread{{ID: "thread-1"}, {ID: "thread-2"}}, nil
	}
	return []protocol.Thread{{ID: "thread-3"}}, nil
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

func TestServiceHandlesSourceRead(t *testing.T) {
	controller := &serviceController{snapshot: turncontrol.Snapshot{Status: protocol.StatusIdle}}
	var published []protocol.Message
	service := NewService(context.Background(), "mac", "test", protocol.Sender{Kind: "device", ID: "mac"}, testCapabilities, controller, serviceInventory{}, func(message protocol.Message) error {
		published = append(published, message)
		return nil
	})
	service.SetSourceReader(serviceSourceReader{snapshot: protocol.SourceSnapshotPayload{
		ProjectID: "project-1", Path: "src/main.go", Content: "package main", StartLine: 1, EndLine: 1, TotalLines: 1, FocusLine: 1,
	}})
	request, _ := protocol.NewMessage(protocol.TypeSourceRead, "source-1", protocol.Sender{Kind: "relay", ID: "run-server"}, protocol.SourceReadPayload{
		ProjectID: "project-1", Path: "src/main.go", FocusLine: 1,
	})
	service.Handle(context.Background(), request)

	if len(published) != 1 || published[0].Type != protocol.TypeSourceSnapshot || published[0].TraceID != "source-1" {
		t.Fatalf("unexpected source response: %#v", published)
	}
	payload, err := protocol.PayloadAs[protocol.SourceSnapshotPayload](published[0])
	if err != nil || payload.Path != "src/main.go" || payload.Content != "package main" {
		t.Fatalf("unexpected source payload: %#v %v", payload, err)
	}
}

func TestServiceRejectsSourceReadWhenReaderUnavailable(t *testing.T) {
	var published []protocol.Message
	service := NewService(context.Background(), "mac", "test", protocol.Sender{Kind: "device", ID: "mac"}, testCapabilities, &serviceController{}, serviceInventory{}, func(message protocol.Message) error {
		published = append(published, message)
		return nil
	})
	request, _ := protocol.NewMessage(protocol.TypeSourceRead, "source-1", protocol.Sender{Kind: "relay", ID: "run-server"}, protocol.SourceReadPayload{ProjectID: "project-1", Path: "main.go"})
	service.Handle(context.Background(), request)

	if len(published) != 1 || published[0].Type != protocol.TypeSourceReadFailed {
		t.Fatalf("unexpected source failure: %#v", published)
	}
	payload, _ := protocol.PayloadAs[protocol.SourceReadFailedPayload](published[0])
	if payload.Code != "SOURCE_UNAVAILABLE" {
		t.Fatalf("unexpected source failure payload: %#v", payload)
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

func TestBootstrapThreadReadTimeoutFallsBackToSessionMetadata(t *testing.T) {
	controller := &serviceController{snapshot: turncontrol.Snapshot{Status: protocol.StatusIdle}}
	service := NewService(
		context.Background(), "mac", "test", protocol.Sender{Kind: "device", ID: "mac"},
		testCapabilities, controller, blockedBootstrapInventory{}, func(protocol.Message) error { return nil },
	)
	service.bootstrapReadTimeout = 10 * time.Millisecond
	updatedAt := time.Now().UTC()
	thread := protocol.Thread{
		ID: "thread-blocked", ProjectID: "project-1", Title: "Available title", Preview: "Available preview",
		Status: "idle", Source: "appServer", UpdatedAt: updatedAt,
	}

	detail := service.readBootstrapThread("project-1", thread)
	if detail.ID != thread.ID || detail.ProjectID != "project-1" || detail.Title != thread.Title || detail.Preview != thread.Preview {
		t.Fatalf("unexpected fallback detail: %#v", detail)
	}
	if !detail.CreatedAt.Equal(updatedAt) || !detail.UpdatedAt.Equal(updatedAt) || len(detail.Turns) != 0 {
		t.Fatalf("fallback timestamps or turns were not preserved: %#v", detail)
	}
}

func TestBootstrapSnapshotFreezesSessionTotal(t *testing.T) {
	service := NewService(
		context.Background(), "mac", "test", protocol.Sender{Kind: "device", ID: "mac"},
		testCapabilities, &serviceController{}, progressBootstrapInventory{}, func(protocol.Message) error { return nil },
	)

	projects, total, err := service.loadBootstrapSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(projects) != 2 || len(projects[0].threads) != 2 || len(projects[1].threads) != 1 {
		t.Fatalf("snapshot projects=%#v total=%d", projects, total)
	}
}

func TestBootstrapReconciliationRequiresAnUnresumedSnapshot(t *testing.T) {
	if !bootstrapReconciliationSafe(-1) {
		t.Fatal("new snapshot should permit reconciliation")
	}
	for _, last := range []int64{0, 1, 100} {
		if bootstrapReconciliationSafe(last) {
			t.Fatalf("resumed snapshot at batch %d permitted reconciliation", last)
		}
	}
}
