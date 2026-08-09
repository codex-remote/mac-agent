package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFixedResolvesGitRoot(t *testing.T) {
	root := t.TempDir()
	command := exec.Command("git", "init", "-q", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewFixed(nested)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Resolve() = %q, want %q", got, want)
	}
}

func TestFixedRejectsNonGitDirectory(t *testing.T) {
	resolver, err := NewFixed(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background()); err == nil {
		t.Fatal("expected non-Git directory error")
	}
}
