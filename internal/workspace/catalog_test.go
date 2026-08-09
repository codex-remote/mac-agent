package workspace

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCatalogListsAndResolvesGitProjects(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "teams", "beta")
	initGit(t, alpha)
	initGit(t, beta)

	catalog, err := NewCatalog([]string{root}, 4)
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
	if projects[0].Name != "alpha" || projects[1].Name != "beta" {
		t.Fatalf("unexpected projects: %#v", projects)
	}
	resolved, err := catalog.Resolve(context.Background(), projects[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(beta)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Path != want {
		t.Fatalf("Resolve() = %q, want %q", resolved.Path, want)
	}
}

func TestCatalogRejectsUnknownProjectID(t *testing.T) {
	root := t.TempDir()
	initGit(t, filepath.Join(root, "alpha"))
	catalog, err := NewCatalog([]string{root}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Resolve(context.Background(), "project_missing"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("Resolve() error = %v, want ErrProjectNotFound", err)
	}
}

func TestCatalogHonorsDepthLimit(t *testing.T) {
	root := t.TempDir()
	initGit(t, filepath.Join(root, "one", "two", "three"))
	catalog, err := NewCatalog([]string{root}, 2)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("List() returned %#v, want no projects", projects)
	}
}

func initGit(t *testing.T, path string) {
	t.Helper()
	command := exec.Command("git", "init", "-q", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v: %s", path, err, output)
	}
}
