package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCodexCatalogListsSidebarProjectsWithinAllowedRoots(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "teams", "beta")
	outside := filepath.Join(t.TempDir(), "outside")
	for _, path := range []string{alpha, beta, outside} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stateFile := writeDesktopState(t, desktopState{
		LocalProjects: map[string]desktopProject{
			"codex-alpha": {Name: "Alpha App", RootPaths: []string{alpha}},
			"codex-beta":  {Name: "Beta Service", RootPaths: []string{beta}},
			"outside":     {Name: "Outside", RootPaths: []string{outside}},
			"removed":     {Name: "Removed", RootPaths: []string{filepath.Join(root, "removed")}},
		},
		ProjectOrder: []string{"codex-beta", "outside", "missing", "codex-alpha"},
		ThreadProjectAssignments: map[string]threadAssignment{
			"thread-beta":  {ProjectKind: "local", ProjectID: "codex-beta", CWD: beta},
			"cloud-thread": {ProjectKind: "cloud", ProjectID: "codex-beta", CWD: beta},
		},
	})

	catalog, err := NewCodexCatalog([]string{root}, stateFile)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("List() returned %d projects, want 2: %#v", len(projects), projects)
	}
	if projects[0].Name != "Beta Service" || projects[1].Name != "Alpha App" {
		t.Fatalf("project order/names = %#v", projects)
	}
	if projects[0].ThreadCWDs["thread-beta"] != beta {
		t.Fatalf("thread assignment = %#v", projects[0].ThreadCWDs)
	}
	if _, ok := projects[0].ThreadCWDs["cloud-thread"]; ok {
		t.Fatalf("non-local assignment was included: %#v", projects[0].ThreadCWDs)
	}
	resolved, err := catalog.Resolve(context.Background(), projects[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Path != alpha {
		t.Fatalf("Resolve() path = %q, want %q", resolved.Path, alpha)
	}
	_, canonicalBeta, err := existingDirectory(beta)
	if err != nil {
		t.Fatal(err)
	}
	if projects[0].ID != projectID(canonicalBeta) {
		t.Fatalf("project ID = %q, want stable path ID", projects[0].ID)
	}
}

func TestCodexCatalogSupportsMultipleRootsAndFiltersAssignments(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	outside := filepath.Join(t.TempDir(), "outside")
	for _, path := range []string{first, second, outside} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stateFile := writeDesktopState(t, desktopState{
		LocalProjects: map[string]desktopProject{"multi": {Name: "Multi", RootPaths: []string{first, outside, second}}},
		ProjectOrder:  []string{"multi"},
		ThreadProjectAssignments: map[string]threadAssignment{
			"inside":  {ProjectKind: "local", ProjectID: "multi", CWD: filepath.Join(second)},
			"outside": {ProjectKind: "local", ProjectID: "multi", CWD: outside},
		},
	})
	catalog, err := NewCodexCatalog([]string{root}, stateFile)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || len(projects[0].RootPaths) != 2 {
		t.Fatalf("projects = %#v", projects)
	}
	if projects[0].WorkingDirectory("inside") != second || projects[0].WorkingDirectory("unknown") != first {
		t.Fatalf("working directories = %#v", projects[0])
	}
	if _, ok := projects[0].ThreadCWDs["outside"]; ok {
		t.Fatalf("outside assignment was included: %#v", projects[0].ThreadCWDs)
	}
}

func TestCodexCatalogRejectsUnknownProjectID(t *testing.T) {
	root := t.TempDir()
	stateFile := writeDesktopState(t, desktopState{LocalProjects: map[string]desktopProject{}, ProjectOrder: []string{}})
	catalog, err := NewCodexCatalog([]string{root}, stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Resolve(context.Background(), "project_missing"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("Resolve() error = %v, want ErrProjectNotFound", err)
	}
}

func TestCodexCatalogRejectsInvalidState(t *testing.T) {
	root := t.TempDir()
	stateFile := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(stateFile, []byte(`{"local-projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewCodexCatalog([]string{root}, stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.List(context.Background()); err == nil {
		t.Fatal("List() error = nil, want missing project-order error")
	}
}

func TestGitProjectCatalogListsOneProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alpha")
	initGit(t, path)
	catalog, err := NewGitProjectCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, canonicalPath, err := existingDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Path != canonicalPath {
		t.Fatalf("List() = %#v", projects)
	}
}

func writeDesktopState(t *testing.T, state desktopState) string {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ".codex-global-state.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func initGit(t *testing.T, path string) {
	t.Helper()
	command := exec.Command("git", "init", "-q", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v: %s", path, err, output)
	}
}
