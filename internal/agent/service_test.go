package agent

import (
	"context"
	"testing"

	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	runcontrol "github.com/ai-coding-remote/mac-agent/internal/run"
)

type serviceController struct {
	started  bool
	snapshot runcontrol.Snapshot
}

func (c *serviceController) Start(_ context.Context, _, _ string, _ runcontrol.EventSink) error {
	c.started = true
	return nil
}

func (c *serviceController) Cancel(string) error           { return nil }
func (c *serviceController) Snapshot() runcontrol.Snapshot { return c.snapshot }

func TestServiceHandlesRunStart(t *testing.T) {
	controller := &serviceController{snapshot: runcontrol.Snapshot{Status: protocol.StatusIdle}}
	service := NewService(context.Background(), "mac", "test", protocol.Sender{Kind: "device", ID: "mac"}, controller, func(protocol.Message) error { return nil })
	message, err := protocol.NewMessage(protocol.TypeRunStart, "run-1", protocol.Sender{Kind: "user", ID: "local"}, protocol.RunStartPayload{RunID: "run-1", Prompt: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	service.Handle(context.Background(), message)
	if !controller.started {
		t.Fatal("controller was not started")
	}
}

func TestServiceInitialMessagesIncludeSnapshot(t *testing.T) {
	controller := &serviceController{snapshot: runcontrol.Snapshot{RunID: "run-1", Status: protocol.StatusRunning, RecentOutput: []string{"line"}}}
	service := NewService(context.Background(), "mac", "test", protocol.Sender{Kind: "device", ID: "mac"}, controller, func(protocol.Message) error { return nil })
	messages := service.InitialMessages()
	if len(messages) != 3 || messages[2].Type != protocol.TypeRunSnapshot {
		t.Fatalf("unexpected initial messages: %#v", messages)
	}
}

func TestServiceInitialMessagesReplayLastTerminal(t *testing.T) {
	terminal, err := protocol.NewMessage(protocol.TypeRunCompleted, "run-1", protocol.Sender{Kind: "device", ID: "mac"}, protocol.RunCompletedPayload{RunID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	controller := &serviceController{snapshot: runcontrol.Snapshot{Status: protocol.StatusIdle, LastTerminal: &terminal}}
	service := NewService(context.Background(), "mac", "test", protocol.Sender{Kind: "device", ID: "mac"}, controller, func(protocol.Message) error { return nil })
	messages := service.InitialMessages()
	if len(messages) != 3 || messages[2].Type != protocol.TypeRunCompleted {
		t.Fatalf("unexpected initial messages: %#v", messages)
	}
}
