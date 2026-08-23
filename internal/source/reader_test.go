package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	"github.com/ai-coding-remote/mac-agent/internal/workspace"
)

type testCatalog struct{ project workspace.Project }

func (c testCatalog) Resolve(_ context.Context, projectID string) (workspace.Project, error) {
	if projectID != c.project.ID {
		return workspace.Project{}, workspace.ErrProjectNotFound
	}
	return c.project, nil
}

func TestReaderReturnsProjectRelativeSourceAndFocusLine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "example.ts")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("const one = 1;\nconst two = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := New(testCatalog{project: workspace.Project{ID: "project-1", Path: root, RootPaths: []string{root}}})

	for _, requested := range []string{"src/example.ts", path} {
		snapshot, err := reader.Read(context.Background(), protocol.SourceReadPayload{ProjectID: "project-1", Path: requested, FocusLine: 2})
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Path != "src/example.ts" || snapshot.FocusLine != 2 || snapshot.StartLine != 1 || snapshot.EndLine != 2 || snapshot.TotalLines != 2 {
			t.Fatalf("unexpected snapshot: %#v", snapshot)
		}
		if snapshot.Content != "const one = 1;\nconst two = 2;" || len(snapshot.SHA256) != 64 {
			t.Fatalf("unexpected source content: %#v", snapshot)
		}
	}
}

func TestReaderRejectsTraversalSymlinkAndSensitiveFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.ts")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, "linked.ts")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=value"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader := New(testCatalog{project: workspace.Project{ID: "project-1", Path: root, RootPaths: []string{root}}})

	for _, requested := range []string{"../outside.ts", filepath.Join(root, "linked.ts"), ".env"} {
		_, err := reader.Read(context.Background(), protocol.SourceReadPayload{ProjectID: "project-1", Path: requested})
		var readErr *ReadError
		if !errors.As(err, &readErr) || (readErr.SourceCode() != "SOURCE_FORBIDDEN" && readErr.SourceCode() != "SOURCE_NOT_FOUND") {
			t.Fatalf("path %q returned %v", requested, err)
		}
	}
}

func TestReaderRejectsBinaryAndBoundsLargeSourceWindow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "binary.dat"), []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("const value = 1;\n", 12000)
	if err := os.WriteFile(filepath.Join(root, "large.ts"), []byte(large), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := New(testCatalog{project: workspace.Project{ID: "project-1", Path: root, RootPaths: []string{root}}})

	if _, err := reader.Read(context.Background(), protocol.SourceReadPayload{ProjectID: "project-1", Path: "binary.dat"}); err == nil {
		t.Fatal("binary source was accepted")
	}
	snapshot, err := reader.Read(context.Background(), protocol.SourceReadPayload{ProjectID: "project-1", Path: "large.ts", FocusLine: 6000, ContextLines: 25})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Truncated || snapshot.StartLine != 5975 || snapshot.EndLine != 6025 || snapshot.FocusLine != 6000 || len(snapshot.Content) > maxResponseBytes {
		t.Fatalf("unexpected bounded snapshot: %#v", snapshot)
	}
}
