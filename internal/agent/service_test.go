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

func (serviceInventory) Projects(context.Context) ([]protocol.Project, error) {
	return []protocol.Project{{ID: "project-1", Name: "alpha", Path: "/work/alpha"}}, nil
}
func (serviceInventory) Threads(context.Context, string) ([]protocol.Thread, error) {
	return []protocol.Thread{{ID: "thread-1", ProjectID: "project-1", Title: "Fix tests"}}, nil
}

func TestServiceHandlesProjectListAndTurnStart(t *testing.T) {
	controller := &serviceController{snapshot: turncontrol.Snapshot{Status: protocol.StatusIdle}}
	var published []protocol.Message
	service := NewService(context.Background(), "mac", "test", protocol.Sender{Kind: "device", ID: "mac"}, controller, serviceInventory{}, func(message protocol.Message) error {
		published = append(published, message)
		return nil
	})
	list, _ := protocol.NewMessage(protocol.TypeProjectList, "projects", protocol.Sender{Kind: "user", ID: "local"}, protocol.ProjectListPayload{})
	service.Handle(context.Background(), list)
	if len(published) != 1 || published[0].Type != protocol.TypeProjectSnapshot {
		t.Fatalf("unexpected project response: %#v", published)
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
	service := NewService(context.Background(), "mac", "test", protocol.Sender{Kind: "device", ID: "mac"}, controller, serviceInventory{}, func(protocol.Message) error { return nil })
	messages := service.InitialMessages()
	if len(messages) != 3 || messages[2].Type != protocol.TypeTurnSnapshot {
		t.Fatalf("unexpected initial messages: %#v", messages)
	}
}
