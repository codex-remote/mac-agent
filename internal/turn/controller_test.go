package turn

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	"github.com/ai-coding-remote/mac-agent/internal/result"
	"github.com/ai-coding-remote/mac-agent/internal/runner"
	"github.com/ai-coding-remote/mac-agent/internal/workspace"
)

type fakeCatalog struct{ project workspace.Project }

func (f fakeCatalog) Resolve(context.Context, string) (workspace.Project, error) {
	return f.project, nil
}

type fakeCollector struct{}

func (fakeCollector) Collect(context.Context, string) (result.GitResult, error) {
	return result.GitResult{ChangedFiles: []string{"greeting.go"}, Diff: "diff"}, nil
}

type fakeRunner struct {
	started chan struct{}
	release chan struct{}
}

func (f *fakeRunner) Run(ctx context.Context, _ runner.Request, emit func(runner.Event)) (runner.Result, error) {
	emit(runner.Event{Kind: runner.EventStarted, ThreadID: "thread-1", TurnID: "turn-1"})
	if f.started != nil {
		close(f.started)
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return runner.Result{ThreadID: "thread-1", TurnID: "turn-1"}, ctx.Err()
		}
	}
	item := protocol.ThreadHistoryItem{ID: "item-agent", Type: "agentMessage", Role: "assistant"}
	emit(runner.Event{Kind: runner.EventItemStarted, ThreadID: "thread-1", TurnID: "turn-1", Item: item})
	emit(runner.Event{Kind: runner.EventItemDelta, ThreadID: "thread-1", TurnID: "turn-1", Stream: "assistant", ItemID: item.ID, Field: "text", Text: "done"})
	item.Text = "done"
	item.Phase = "final_answer"
	emit(runner.Event{Kind: runner.EventItemCompleted, ThreadID: "thread-1", TurnID: "turn-1", Item: item})
	emit(runner.Event{Kind: runner.EventOutput, ThreadID: "thread-1", TurnID: "turn-1", Stream: "assistant", Text: "done"})
	return runner.Result{ThreadID: "thread-1", TurnID: "turn-1", Duration: time.Millisecond, Summary: "done"}, nil
}

func TestControllerEmitsOrderedItemEvents(t *testing.T) {
	controller := NewController(&fakeRunner{}, fakeCatalog{workspace.Project{ID: "project-1", Path: t.TempDir()}}, fakeCollector{}, time.Minute, 10, protocol.Sender{Kind: "device", ID: "mac"})
	messages := make(chan protocol.Message, 8)
	if err := controller.Start(context.Background(), "trace-items", protocol.TurnStartPayload{ProjectID: "project-1", Prompt: "stream"}, func(message protocol.Message) {
		messages <- message
	}); err != nil {
		t.Fatal(err)
	}
	sequences := []int64{}
	deadline := time.After(time.Second)
	for len(sequences) < 3 {
		select {
		case message := <-messages:
			switch message.Type {
			case protocol.TypeTurnItemStarted:
				payload, _ := protocol.PayloadAs[protocol.TurnItemStartedPayload](message)
				sequences = append(sequences, payload.Sequence)
			case protocol.TypeTurnItemDelta:
				payload, _ := protocol.PayloadAs[protocol.TurnItemDeltaPayload](message)
				if payload.ItemID != "item-agent" || payload.Delta != "done" {
					t.Fatalf("unexpected delta: %#v", payload)
				}
				sequences = append(sequences, payload.Sequence)
			case protocol.TypeTurnItemDone:
				payload, _ := protocol.PayloadAs[protocol.TurnItemCompletedPayload](message)
				sequences = append(sequences, payload.Sequence)
			}
		case <-deadline:
			t.Fatal("timed out waiting for item events")
		}
	}
	if sequences[0] != 1 || sequences[1] != 2 || sequences[2] != 3 {
		t.Fatalf("unexpected sequences: %#v", sequences)
	}
}

func TestControllerCompletesAndReturnsToIdle(t *testing.T) {
	controller := NewController(&fakeRunner{}, fakeCatalog{workspace.Project{ID: "project-1", Path: t.TempDir()}}, fakeCollector{}, time.Minute, 10, protocol.Sender{Kind: "device", ID: "mac"})
	terminal := make(chan protocol.Message, 1)
	if err := controller.Start(context.Background(), "trace-1", protocol.TurnStartPayload{ProjectID: "project-1", Prompt: "fix it"}, func(message protocol.Message) {
		if message.Type == protocol.TypeTurnCompleted {
			terminal <- message
		}
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-terminal:
		payload, err := protocol.PayloadAs[protocol.TurnCompletedPayload](message)
		if err != nil {
			t.Fatal(err)
		}
		if payload.ThreadID != "thread-1" || payload.Summary != "done" || len(payload.ChangedFiles) != 1 {
			t.Fatalf("unexpected payload: %#v", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for completion")
	}
	eventually(t, func() bool { return controller.Snapshot().Status == protocol.StatusIdle })
}

func TestControllerRejectsConcurrentTurnAndInterruptsCurrent(t *testing.T) {
	fake := &fakeRunner{started: make(chan struct{}), release: make(chan struct{})}
	controller := NewController(fake, fakeCatalog{workspace.Project{ID: "project-1", Path: t.TempDir()}}, fakeCollector{}, time.Minute, 10, protocol.Sender{Kind: "device", ID: "mac"})
	interrupted := make(chan struct{}, 1)
	var once sync.Once
	start := protocol.TurnStartPayload{ProjectID: "project-1", Prompt: "wait"}
	if err := controller.Start(context.Background(), "trace-1", start, func(message protocol.Message) {
		if message.Type == protocol.TypeTurnInterrupted {
			once.Do(func() { interrupted <- struct{}{} })
		}
	}); err != nil {
		t.Fatal(err)
	}
	<-fake.started
	if err := controller.Start(context.Background(), "trace-2", start, nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start() error = %v, want ErrBusy", err)
	}
	if err := controller.Interrupt("thread-1", "turn-1"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-interrupted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for interruption")
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met")
}
