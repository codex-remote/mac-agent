package run

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	"github.com/ai-coding-remote/mac-agent/internal/result"
	"github.com/ai-coding-remote/mac-agent/internal/runner"
)

type fakeWorkspace struct{ path string }

func (f fakeWorkspace) Resolve(context.Context) (string, error) { return f.path, nil }

type fakeCollector struct{}

func (fakeCollector) Collect(context.Context, string) (result.GitResult, error) {
	return result.GitResult{ChangedFiles: []string{"greeting.go"}, Diff: "diff"}, nil
}

type fakeRunner struct {
	started chan struct{}
	release chan struct{}
}

func (f *fakeRunner) Run(ctx context.Context, _ runner.Request, emit func(runner.Output)) (runner.Result, error) {
	if f.started != nil {
		close(f.started)
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return runner.Result{}, ctx.Err()
		}
	}
	emit(runner.Output{Stream: "stdout", Text: "done\n"})
	return runner.Result{ExitCode: 0, Duration: time.Millisecond, Summary: "done"}, nil
}

func TestControllerCompletesAndReturnsToIdle(t *testing.T) {
	controller := NewController(&fakeRunner{}, fakeWorkspace{path: t.TempDir()}, fakeCollector{}, time.Minute, 10, protocol.Sender{Kind: "device", ID: "mac"})
	terminal := make(chan protocol.Message, 1)
	if err := controller.Start(context.Background(), "run-1", "fix it", func(message protocol.Message) {
		if message.Type == protocol.TypeRunCompleted {
			terminal <- message
		}
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-terminal:
		payload, err := protocol.PayloadAs[protocol.RunCompletedPayload](message)
		if err != nil {
			t.Fatal(err)
		}
		if payload.Summary != "done" || len(payload.ChangedFiles) != 1 {
			t.Fatalf("unexpected payload: %#v", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for completion")
	}
	eventually(t, func() bool { return controller.Snapshot().Status == protocol.StatusIdle })
	snapshot := controller.Snapshot()
	if snapshot.LastTerminal == nil || snapshot.LastTerminal.Type != protocol.TypeRunCompleted {
		t.Fatalf("last terminal = %#v, want run.completed", snapshot.LastTerminal)
	}
}

func TestControllerRejectsConcurrentRunAndCancelsCurrent(t *testing.T) {
	fake := &fakeRunner{started: make(chan struct{}), release: make(chan struct{})}
	controller := NewController(fake, fakeWorkspace{path: t.TempDir()}, fakeCollector{}, time.Minute, 10, protocol.Sender{Kind: "device", ID: "mac"})
	cancelled := make(chan struct{}, 1)
	var once sync.Once
	if err := controller.Start(context.Background(), "run-1", "wait", func(message protocol.Message) {
		if message.Type == protocol.TypeRunCancelled {
			once.Do(func() { cancelled <- struct{}{} })
		}
	}); err != nil {
		t.Fatal(err)
	}
	<-fake.started
	if err := controller.Start(context.Background(), "run-2", "second", nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start() error = %v, want ErrBusy", err)
	}
	if err := controller.Cancel("run-1"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cancellation")
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
