package inventory

import (
	"context"
	"sort"
	"time"

	"github.com/ai-coding-remote/mac-agent/internal/codexapp"
	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	"github.com/ai-coding-remote/mac-agent/internal/workspace"
)

type ProjectCatalog interface {
	List(context.Context) ([]workspace.Project, error)
	Resolve(context.Context, string) (workspace.Project, error)
}

type ThreadLister interface {
	ListThreads(context.Context, string) ([]codexapp.Thread, error)
}

type Service struct {
	catalog ProjectCatalog
	threads ThreadLister
}

func New(catalog ProjectCatalog, threads ThreadLister) *Service {
	return &Service{catalog: catalog, threads: threads}
}

func (s *Service) Projects(ctx context.Context) ([]protocol.Project, error) {
	projects, err := s.catalog.List(ctx)
	if err != nil {
		return nil, err
	}
	threads, err := s.threads.ListThreads(ctx, "")
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	updated := make(map[string]time.Time)
	for _, thread := range threads {
		counts[thread.CWD]++
		value := codexapp.UnixTime(thread.UpdatedAt)
		if value.After(updated[thread.CWD]) {
			updated[thread.CWD] = value
		}
	}
	response := make([]protocol.Project, 0, len(projects))
	for _, project := range projects {
		item := protocol.Project{ID: project.ID, Name: project.Name, Path: project.Path, ThreadCount: counts[project.Path]}
		if value := updated[project.Path]; !value.IsZero() {
			item.UpdatedAt = &value
		}
		response = append(response, item)
	}
	return response, nil
}

func (s *Service) Threads(ctx context.Context, projectID string) ([]protocol.Thread, error) {
	project, err := s.catalog.Resolve(ctx, projectID)
	if err != nil {
		return nil, err
	}
	threads, err := s.threads.ListThreads(ctx, project.Path)
	if err != nil {
		return nil, err
	}
	response := make([]protocol.Thread, 0, len(threads))
	for _, thread := range threads {
		response = append(response, protocol.Thread{
			ID: thread.ID, ProjectID: project.ID, Title: thread.Title(), Preview: thread.Preview,
			Status: thread.StatusName(), Source: thread.SourceName(), UpdatedAt: codexapp.UnixTime(thread.UpdatedAt),
		})
	}
	sort.Slice(response, func(i, j int) bool { return response[i].UpdatedAt.After(response[j].UpdatedAt) })
	return response, nil
}
