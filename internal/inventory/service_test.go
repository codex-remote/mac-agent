package inventory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ai-coding-remote/mac-agent/internal/codexapp"
	"github.com/ai-coding-remote/mac-agent/internal/workspace"
)

type fakeCatalog struct{ projects []workspace.Project }

func (f fakeCatalog) List(context.Context) ([]workspace.Project, error) { return f.projects, nil }
func (f fakeCatalog) Resolve(_ context.Context, id string) (workspace.Project, error) {
	for _, project := range f.projects {
		if project.ID == id {
			return project, nil
		}
	}
	return workspace.Project{}, workspace.ErrProjectNotFound
}

type fakeThreads struct{ values []codexapp.Thread }

func (f fakeThreads) ListThreads(_ context.Context, cwd string) ([]codexapp.Thread, error) {
	if cwd == "" {
		return f.values, nil
	}
	var filtered []codexapp.Thread
	for _, thread := range f.values {
		if thread.CWD == cwd {
			filtered = append(filtered, thread)
		}
	}
	return filtered, nil
}

func TestServiceBuildsProjectAndThreadSnapshots(t *testing.T) {
	status, _ := json.Marshal(map[string]any{"type": "idle"})
	source, _ := json.Marshal("cli")
	service := New(fakeCatalog{[]workspace.Project{{ID: "project-1", Name: "alpha", Path: "/work/alpha"}}}, fakeThreads{[]codexapp.Thread{{
		ID: "thread-1", CWD: "/work/alpha", Preview: "Fix login", Status: status, Source: source, UpdatedAt: 10,
	}}})
	projects, err := service.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].ThreadCount != 1 || projects[0].UpdatedAt == nil {
		t.Fatalf("unexpected projects: %#v", projects)
	}
	threads, err := service.Threads(context.Background(), "project-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].Title != "Fix login" || threads[0].Status != "idle" {
		t.Fatalf("unexpected threads: %#v", threads)
	}
}
