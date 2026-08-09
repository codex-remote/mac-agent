package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCodexBuildsSafeCommandAndStreamsOutput(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "fake-codex")
	content := "#!/bin/sh\nprintf '%s\\n' \"$@\" > args.txt\necho progress >&2\necho final-answer\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	var outputs []Output
	var outputsMu sync.Mutex
	result, err := (Codex{Binary: script}).Run(context.Background(), Request{
		RunID:      "run-1",
		Prompt:     "fix the greeting",
		WorkingDir: root,
	}, func(output Output) {
		outputsMu.Lock()
		defer outputsMu.Unlock()
		outputs = append(outputs, output)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Summary != "final-answer" {
		t.Fatalf("unexpected result: %#v", result)
	}
	args, err := os.ReadFile(filepath.Join(root, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	joined := string(args)
	for _, expected := range []string{"exec", "--ephemeral", "--sandbox", "workspace-write", "--cd", root, "fix the greeting"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("args missing %q: %s", expected, joined)
		}
	}
	if len(outputs) != 2 {
		t.Fatalf("outputs = %#v", outputs)
	}
}

func TestCodexCancellationTerminatesProcess(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "slow-codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := (Codex{Binary: script}).Run(ctx, Request{Prompt: "wait", WorkingDir: root}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestCodexReturnsStartError(t *testing.T) {
	_, err := (Codex{Binary: filepath.Join(t.TempDir(), "missing-codex")}).Run(context.Background(), Request{Prompt: "test", WorkingDir: t.TempDir()}, nil)
	var startErr *StartError
	if !errors.As(err, &startErr) {
		t.Fatalf("error = %v, want StartError", err)
	}
}
