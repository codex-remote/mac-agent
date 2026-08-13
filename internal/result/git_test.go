package result

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCollectGit(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.name", "Test")
	runGit(t, root, "config", "user.email", "test@example.com")
	tracked := filepath.Join(root, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-qm", "initial")
	if err := os.WriteFile(tracked, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := CollectGit(context.Background(), root, DefaultMaxDiffBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(result.ChangedFiles, []string{"new.txt", "tracked.txt"}) {
		t.Fatalf("changed files = %#v", result.ChangedFiles)
	}
	if !strings.Contains(result.Diff, "-before") || !strings.Contains(result.Diff, "+after") {
		t.Fatalf("unexpected diff: %s", result.Diff)
	}
}

func TestCollectGitAggregatesNestedRepositories(t *testing.T) {
	workspace := t.TempDir()
	first := filepath.Join(workspace, "first")
	second := filepath.Join(workspace, "services", "second")
	for _, repository := range []string{first, second} {
		initRepositoryWithChange(t, repository)
	}

	result, err := CollectGit(context.Background(), workspace, DefaultMaxDiffBytes)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"first/tracked.txt", "services/second/tracked.txt"}
	if !slices.Equal(result.ChangedFiles, wantFiles) {
		t.Fatalf("changed files = %#v, want %#v", result.ChangedFiles, wantFiles)
	}
	if !strings.Contains(result.Diff, "a/first/tracked.txt") || !strings.Contains(result.Diff, "a/services/second/tracked.txt") {
		t.Fatalf("nested repository prefixes missing from diff: %s", result.Diff)
	}
}

func TestCollectGitAllowsProjectWithoutRepository(t *testing.T) {
	result, err := CollectGit(context.Background(), t.TempDir(), DefaultMaxDiffBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ChangedFiles) != 0 || result.Diff != "" {
		t.Fatalf("result = %#v, want empty", result)
	}
}

func initRepositoryWithChange(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.name", "Test")
	runGit(t, root, "config", "user.email", "test@example.com")
	tracked := filepath.Join(root, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-qm", "initial")
	if err := os.WriteFile(tracked, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", root}, args...)
	if output, err := exec.Command("git", commandArgs...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
