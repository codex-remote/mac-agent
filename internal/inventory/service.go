package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ai-coding-remote/mac-agent/internal/codexapp"
	"github.com/ai-coding-remote/mac-agent/internal/codexitem"
	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	"github.com/ai-coding-remote/mac-agent/internal/workspace"
)

type ProjectCatalog interface {
	List(context.Context) ([]workspace.Project, error)
}

type ThreadLister interface {
	ListThreads(context.Context, string) ([]codexapp.Thread, error)
	ListLatestTurns(context.Context, string, int) ([]codexapp.Turn, error)
	ReadThread(context.Context, string) (codexapp.Thread, error)
	ListPermissionProfiles(context.Context, string) ([]codexapp.PermissionProfile, error)
}

func (s *Service) ExecutionProfiles(ctx context.Context, projectID string) (protocol.ExecutionProfileSnapshotPayload, error) {
	projects, err := s.catalog.List(ctx)
	if err != nil {
		return protocol.ExecutionProfileSnapshotPayload{}, err
	}
	projectIndex := projectIndexByID(projects, projectID)
	if projectIndex < 0 {
		return protocol.ExecutionProfileSnapshotPayload{}, fmt.Errorf("%w: %s", workspace.ErrProjectNotFound, projectID)
	}
	profiles, err := s.threads.ListPermissionProfiles(ctx, projects[projectIndex].Path)
	if err != nil {
		return protocol.ExecutionProfileSnapshotPayload{}, err
	}
	items := make([]protocol.ExecutionProfile, 0, len(profiles))
	defaultProfileID := ""
	for _, profile := range profiles {
		description := ""
		if profile.Description != nil {
			description = *profile.Description
		}
		items = append(items, protocol.ExecutionProfile{ID: profile.ID, Description: description, Allowed: profile.Allowed})
		if profile.Allowed && (defaultProfileID == "" || profile.ID == ":workspace") {
			defaultProfileID = profile.ID
		}
	}
	return protocol.ExecutionProfileSnapshotPayload{
		ProjectID: projectID, DefaultProfileID: defaultProfileID, Profiles: items,
	}, nil
}

const (
	maxThreadDetailBytes   = 200 * 1024
	maxHistoryFieldBytes   = 24 * 1024
	maxLatestPreviewBytes  = 512
	latestPreviewTurnLimit = 3
	latestPreviewWorkers   = 4
	latestPreviewCacheSize = 512
	latestPreviewTimeout   = 3 * time.Second
)

type latestPreviewEntry struct {
	updatedAt int64
	text      string
}

type Service struct {
	catalog ProjectCatalog
	threads ThreadLister

	previewMu    sync.Mutex
	previewCache map[string]latestPreviewEntry
}

func New(catalog ProjectCatalog, threads ThreadLister) *Service {
	return &Service{catalog: catalog, threads: threads, previewCache: make(map[string]latestPreviewEntry)}
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
	counts := make([]int, len(projects))
	updated := make([]time.Time, len(projects))
	for _, thread := range threads {
		owner := projectOwner(projects, thread)
		if owner < 0 {
			continue
		}
		counts[owner]++
		value := codexapp.UnixTime(thread.UpdatedAt)
		if value.After(updated[owner]) {
			updated[owner] = value
		}
	}
	response := make([]protocol.Project, 0, len(projects))
	for index, project := range projects {
		item := protocol.Project{ID: project.ID, Name: project.Name, Path: project.Path, ThreadCount: counts[index]}
		if value := updated[index]; !value.IsZero() {
			item.UpdatedAt = &value
		}
		response = append(response, item)
	}
	return response, nil
}

func (s *Service) Threads(ctx context.Context, projectID string) ([]protocol.Thread, error) {
	projects, err := s.catalog.List(ctx)
	if err != nil {
		return nil, err
	}
	projectIndex := -1
	for index := range projects {
		if projects[index].ID == projectID {
			projectIndex = index
			break
		}
	}
	if projectIndex < 0 {
		return nil, fmt.Errorf("%w: %s", workspace.ErrProjectNotFound, projectID)
	}
	project := projects[projectIndex]
	threads, err := s.threads.ListThreads(ctx, "")
	if err != nil {
		return nil, err
	}
	owned := make([]codexapp.Thread, 0, len(threads))
	for _, thread := range threads {
		if projectOwner(projects, thread) != projectIndex {
			continue
		}
		owned = append(owned, thread)
	}
	response := make([]protocol.Thread, len(owned))
	workerCount := min(latestPreviewWorkers, len(owned))
	jobs := make(chan int)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				thread := owned[index]
				response[index] = protocol.Thread{
					ID: thread.ID, ProjectID: project.ID, Title: thread.Title(), Preview: thread.Preview,
					LatestMessagePreview: s.latestMessagePreview(ctx, thread), Status: thread.StatusName(),
					Source: thread.SourceName(), UpdatedAt: codexapp.UnixTime(thread.UpdatedAt),
				}
			}
		}()
	}
	for index := range owned {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	sort.Slice(response, func(i, j int) bool { return response[i].UpdatedAt.After(response[j].UpdatedAt) })
	return response, nil
}

func (s *Service) latestMessagePreview(ctx context.Context, thread codexapp.Thread) string {
	fallback := compactPreview(thread.Preview)
	s.previewMu.Lock()
	if cached, ok := s.previewCache[thread.ID]; ok && cached.updatedAt == thread.UpdatedAt {
		s.previewMu.Unlock()
		return cached.text
	}
	s.previewMu.Unlock()

	previewContext, cancel := context.WithTimeout(ctx, latestPreviewTimeout)
	defer cancel()
	turns, err := s.threads.ListLatestTurns(previewContext, thread.ID, latestPreviewTurnLimit)
	if err != nil {
		return fallback
	}
	preview := latestConversationText(turns)
	if preview == "" {
		preview = fallback
	}
	s.previewMu.Lock()
	if len(s.previewCache) >= latestPreviewCacheSize {
		for key := range s.previewCache {
			delete(s.previewCache, key)
			break
		}
	}
	s.previewCache[thread.ID] = latestPreviewEntry{updatedAt: thread.UpdatedAt, text: preview}
	s.previewMu.Unlock()
	return preview
}

func latestConversationText(turns []codexapp.Turn) string {
	for _, turn := range turns {
		for index := len(turn.Items) - 1; index >= 0; index-- {
			item, ok := codexitem.Normalize(turn.Items[index], maxLatestPreviewBytes)
			if !ok || (item.Type != "userMessage" && item.Type != "agentMessage") {
				continue
			}
			if item.Type == "agentMessage" && item.Phase == "commentary" {
				continue
			}
			if text := compactPreview(item.Text); text != "" {
				return text
			}
		}
	}
	return ""
}

func compactPreview(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value, _ = truncateUTF8(value, maxLatestPreviewBytes)
	return value
}

func (s *Service) ReadThread(ctx context.Context, projectID, threadID string) (protocol.ThreadDetailPayload, error) {
	projects, err := s.catalog.List(ctx)
	if err != nil {
		return protocol.ThreadDetailPayload{}, err
	}
	projectIndex := projectIndexByID(projects, projectID)
	if projectIndex < 0 {
		return protocol.ThreadDetailPayload{}, fmt.Errorf("%w: %s", workspace.ErrProjectNotFound, projectID)
	}
	thread, err := s.threads.ReadThread(ctx, threadID)
	if err != nil {
		return protocol.ThreadDetailPayload{}, err
	}
	if thread.ID != threadID || projectOwner(projects, thread) != projectIndex {
		return protocol.ThreadDetailPayload{}, fmt.Errorf("thread %s does not belong to project %s", threadID, projectID)
	}

	title, titleTruncated := truncateUTF8(thread.Title(), maxHistoryFieldBytes)
	preview, previewTruncated := truncateUTF8(thread.Preview, maxHistoryFieldBytes)
	detail := protocol.ThreadDetail{
		ID: thread.ID, ProjectID: projectID, Title: title, Preview: preview,
		Status: thread.StatusName(), Source: thread.SourceName(), CreatedAt: codexapp.UnixTime(thread.CreatedAt),
		UpdatedAt: codexapp.UnixTime(thread.UpdatedAt), Turns: make([]protocol.ThreadHistoryTurn, 0, len(thread.Turns)),
	}
	for _, turn := range thread.Turns {
		detail.Turns = append(detail.Turns, normalizeTurn(turn))
	}
	payload := protocol.ThreadDetailPayload{ProjectID: projectID, Thread: detail, Truncated: titleTruncated || previewTruncated}
	fitThreadDetail(&payload)
	return payload, nil
}

func projectIndexByID(projects []workspace.Project, projectID string) int {
	for index := range projects {
		if projects[index].ID == projectID {
			return index
		}
	}
	return -1
}

type rawThreadItem = codexitem.RawItem

func normalizeTurn(turn codexapp.Turn) protocol.ThreadHistoryTurn {
	result := protocol.ThreadHistoryTurn{
		ID: turn.ID, Status: turn.Status, DurationMS: turn.DurationMS,
		Items: make([]protocol.ThreadHistoryItem, 0, len(turn.Items)),
	}
	if turn.StartedAt != nil {
		value := codexapp.UnixTime(*turn.StartedAt)
		result.StartedAt = &value
	}
	if turn.CompletedAt != nil {
		value := codexapp.UnixTime(*turn.CompletedAt)
		result.CompletedAt = &value
	}
	if turn.Error != nil {
		var clipped bool
		result.Error, clipped = truncateUTF8(turn.Error.Message, maxHistoryFieldBytes)
		result.Truncated = clipped
	}
	for _, raw := range turn.Items {
		if item, ok := codexitem.Normalize(raw, maxHistoryFieldBytes); ok {
			result.Items = append(result.Items, item)
		}
	}
	return result
}

func normalizeItem(source rawThreadItem) protocol.ThreadHistoryItem {
	return codexitem.NormalizeRaw(source, maxHistoryFieldBytes)
}

func truncateUTF8(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	const suffix = "..."
	end := limit - len(suffix)
	for end > 0 && value[end]&0xc0 == 0x80 {
		end--
	}
	return value[:end] + suffix, true
}

func fitThreadDetail(payload *protocol.ThreadDetailPayload) {
	for encodedSize(payload) > maxThreadDetailBytes {
		payload.Truncated = true
		turns := payload.Thread.Turns
		if len(turns) > 1 {
			payload.Thread.Turns = turns[1:]
			continue
		}
		if len(turns) == 0 {
			return
		}
		turn := &payload.Thread.Turns[0]
		turn.Truncated = true
		if len(turn.Items) > 1 {
			turn.Items = turn.Items[1:]
			continue
		}
		if len(turn.Items) == 0 {
			return
		}
		item := &turn.Items[0]
		item.Truncated = true
		if len(item.Changes) > 1 {
			item.Changes = item.Changes[1:]
			continue
		}
		item.Text, _ = truncateUTF8(item.Text, 4096)
		item.Output, _ = truncateUTF8(item.Output, 4096)
		if len(item.Changes) == 1 {
			item.Changes[0].Diff, _ = truncateUTF8(item.Changes[0].Diff, 4096)
		}
		return
	}
}

func encodedSize(value any) int {
	data, _ := json.Marshal(value)
	return len(data)
}

func projectOwner(projects []workspace.Project, thread codexapp.Thread) int {
	for index, project := range projects {
		if _, assigned := project.ThreadCWDs[thread.ID]; assigned {
			return index
		}
	}
	owner := -1
	longestMatch := -1
	for index, project := range projects {
		if match := project.MatchPath(thread.CWD); match > longestMatch {
			owner = index
			longestMatch = match
		}
	}
	return owner
}
