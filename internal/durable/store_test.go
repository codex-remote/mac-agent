package durable

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/codex-remote/mac-agent/internal/protocol"
)

func TestRunOutboxSurvivesReopenUntilDurableAck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite3")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	payload := protocol.TurnStartPayload{CommandID: "cmd-1", RunID: "run-1", ProjectID: "project-1", Prompt: "test"}
	created, err := store.PrepareRun(context.Background(), "run-1", "cmd-1", payload)
	if err != nil || !created {
		t.Fatalf("prepare: created=%v err=%v", created, err)
	}
	message, _ := protocol.NewMessage(protocol.TypeRunAccepted, "run-1", protocol.Sender{Kind: "device", ID: "local-mac"}, protocol.RunAcceptedPayload{CommandID: "cmd-1", RunID: "run-1"})
	message, err = store.AppendEvent(context.Background(), "run-1", message)
	if err != nil {
		t.Fatal(err)
	}
	if message.AgentSequence != 1 {
		t.Fatalf("sequence=%d", message.AgentSequence)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pending, err := store.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].AgentSequence != 1 {
		t.Fatalf("pending=%#v", pending)
	}
	if err := store.DurableAck(context.Background(), "run-1", 1); err != nil {
		t.Fatal(err)
	}
	pending, err = store.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after ack=%#v", pending)
	}
}

func TestPrepareRunDeduplicatesAndRejectsConflict(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "runtime.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	payload := protocol.TurnStartPayload{CommandID: "cmd-1", RunID: "run-1", ProjectID: "project-1", Prompt: "one"}
	if created, err := store.PrepareRun(ctx, "run-1", "cmd-1", payload); err != nil || !created {
		t.Fatalf("first=%v %v", created, err)
	}
	if created, err := store.PrepareRun(ctx, "run-1", "cmd-1", payload); err != nil || created {
		t.Fatalf("duplicate=%v %v", created, err)
	}
	payload.Prompt = "two"
	if _, err := store.PrepareRun(ctx, "run-1", "cmd-1", payload); err == nil {
		t.Fatal("conflicting payload accepted")
	}
}
