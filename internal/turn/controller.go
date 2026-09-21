package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/codex-remote/mac-agent/internal/buffer"
	"github.com/codex-remote/mac-agent/internal/protocol"
	"github.com/codex-remote/mac-agent/internal/result"
	"github.com/codex-remote/mac-agent/internal/runner"
	"github.com/codex-remote/mac-agent/internal/workspace"
)

const (
	MaxPromptBytes        = 16 * 1024
	maxLiveItemFieldBytes = 24 * 1024
	maxLiveSnapshotBytes  = 180 * 1024
)

var (
	ErrBusy         = errors.New("agent is already running a turn")
	ErrTurnNotFound = errors.New("turn not found")
)

type ProjectCatalog interface {
	Resolve(context.Context, string) (workspace.Project, error)
}

type GitCollector interface {
	Collect(context.Context, string) (result.GitResult, error)
}

type EventSink func(protocol.Message)

type Snapshot struct {
	TraceID            string
	ProjectID          string
	ThreadID           string
	TurnID             string
	Status             string
	StartedAt          time.Time
	RecentOutput       []string
	LiveItems          []protocol.ThreadHistoryItem
	LastSequence       int64
	LiveItemsTruncated bool
	LastTerminal       *protocol.Message
}

type Controller struct {
	mu            sync.RWMutex
	runner        runner.Runner
	catalog       ProjectCatalog
	collector     GitCollector
	timeout       time.Duration
	sender        protocol.Sender
	logs          *buffer.Ring
	liveItems     []protocol.ThreadHistoryItem
	liveItemIndex map[string]int
	lastSequence  int64
	traceID       string
	projectID     string
	threadID      string
	turnID        string
	startedAt     time.Time
	cancel        context.CancelFunc
	lastTerminal  *protocol.Message
}

func NewController(turnRunner runner.Runner, catalog ProjectCatalog, collector GitCollector, timeout time.Duration, logLines int, sender protocol.Sender) *Controller {
	return &Controller{
		runner: turnRunner, catalog: catalog, collector: collector, timeout: timeout, sender: sender, logs: buffer.New(logLines),
	}
}

func (c *Controller) Start(parent context.Context, traceID string, payload protocol.TurnStartPayload, sink EventSink) error {
	if traceID == "" {
		return fmt.Errorf("trace_id is required")
	}
	if payload.ProjectID == "" {
		return fmt.Errorf("project_id is required")
	}
	if payload.Prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	if len(payload.Prompt) > MaxPromptBytes {
		return fmt.Errorf("prompt exceeds %d bytes", MaxPromptBytes)
	}
	project, err := c.catalog.Resolve(parent, payload.ProjectID)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.traceID != "" {
		c.mu.Unlock()
		return ErrBusy
	}
	c.logs.Reset()
	startedAt := time.Now().UTC()
	var turnContext context.Context
	var cancel context.CancelFunc
	if c.timeout > 0 {
		turnContext, cancel = context.WithTimeout(parent, c.timeout)
	} else {
		turnContext, cancel = context.WithCancel(parent)
	}
	c.traceID = traceID
	c.projectID = project.ID
	c.threadID = payload.ThreadID
	c.turnID = ""
	c.startedAt = startedAt
	c.cancel = cancel
	c.lastTerminal = nil
	c.liveItems = nil
	c.liveItemIndex = make(map[string]int)
	c.lastSequence = 0
	c.mu.Unlock()

	c.emit(sink, protocol.TypeAgentStatus, traceID, protocol.AgentStatusPayload{
		Status: protocol.StatusRunning, ProjectID: project.ID, ThreadID: payload.ThreadID,
	})
	go c.execute(turnContext, traceID, project, payload, startedAt, sink)
	return nil
}

func (c *Controller) Interrupt(threadID, turnID string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.traceID == "" || c.cancel == nil || c.threadID != threadID || c.turnID != turnID {
		return ErrTurnNotFound
	}
	c.cancel()
	return nil
}

func (c *Controller) Close() {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	status := protocol.StatusIdle
	if c.traceID != "" {
		status = protocol.StatusRunning
	}
	liveItems, liveItemsTruncated := fitLiveSnapshot(c.liveItems)
	snapshot := Snapshot{
		TraceID: c.traceID, ProjectID: c.projectID, ThreadID: c.threadID, TurnID: c.turnID,
		Status: status, StartedAt: c.startedAt, RecentOutput: c.logs.Values(), LiveItems: liveItems,
		LastSequence: c.lastSequence, LiveItemsTruncated: liveItemsTruncated,
	}
	if c.lastTerminal != nil {
		terminal := *c.lastTerminal
		snapshot.LastTerminal = &terminal
	}
	return snapshot
}

func (c *Controller) execute(ctx context.Context, traceID string, project workspace.Project, payload protocol.TurnStartPayload, startedAt time.Time, sink EventSink) {
	workingDir := project.WorkingDirectory(payload.ThreadID)
	turnResult, turnErr := c.runner.Run(ctx, runner.Request{
		ProjectID: project.ID, ThreadID: payload.ThreadID, Prompt: payload.Prompt, WorkingDir: workingDir,
		PermissionProfileID: payload.PermissionProfileID,
	}, func(event runner.Event) {
		switch event.Kind {
		case runner.EventStarted:
			c.mu.Lock()
			if c.traceID == traceID {
				c.threadID = event.ThreadID
				c.turnID = event.TurnID
			}
			c.mu.Unlock()
			c.emit(sink, protocol.TypeTurnStarted, traceID, protocol.TurnStartedPayload{
				ProjectID: project.ID, ThreadID: event.ThreadID, TurnID: event.TurnID, StartedAt: startedAt,
			})
		case runner.EventOutput:
			c.logs.Add(event.Text)
			c.emit(sink, protocol.TypeTurnOutput, traceID, protocol.TurnOutputPayload{
				ProjectID: project.ID, ThreadID: event.ThreadID, TurnID: event.TurnID, Stream: event.Stream, Text: event.Text,
			})
		case runner.EventItemStarted:
			sequence := c.storeStartedItem(traceID, event.Item)
			if sequence == 0 {
				return
			}
			c.emit(sink, protocol.TypeTurnItemStarted, traceID, protocol.TurnItemStartedPayload{
				ProjectID: project.ID, ThreadID: event.ThreadID, TurnID: event.TurnID, Sequence: sequence, Item: event.Item,
			})
		case runner.EventItemDelta:
			sequence := c.storeItemDelta(traceID, event)
			if sequence == 0 {
				return
			}
			c.emit(sink, protocol.TypeTurnItemDelta, traceID, protocol.TurnItemDeltaPayload{
				ProjectID: project.ID, ThreadID: event.ThreadID, TurnID: event.TurnID, Sequence: sequence,
				ItemID: event.ItemID, Field: event.Field, Delta: event.Text,
			})
		case runner.EventItemCompleted:
			sequence := c.storeCompletedItem(traceID, event.Item)
			if sequence == 0 {
				return
			}
			c.emit(sink, protocol.TypeTurnItemDone, traceID, protocol.TurnItemCompletedPayload{
				ProjectID: project.ID, ThreadID: event.ThreadID, TurnID: event.TurnID, Sequence: sequence, Item: event.Item,
			})
		}
	})
	duration := time.Since(startedAt)
	threadID := turnResult.ThreadID
	if threadID == "" {
		threadID = payload.ThreadID
	}
	turnID := turnResult.TurnID
	var terminal protocol.Message

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		terminal = c.emit(sink, protocol.TypeTurnFailed, traceID, protocol.TurnFailedPayload{
			ProjectID: project.ID, ThreadID: threadID, TurnID: turnID, Code: "TURN_TIMEOUT", Message: "Codex turn timed out", DurationMS: duration.Milliseconds(),
		})
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(turnErr, runner.ErrTurnInterrupted):
		terminal = c.emit(sink, protocol.TypeTurnInterrupted, traceID, protocol.TurnInterruptedPayload{
			ProjectID: project.ID, ThreadID: threadID, TurnID: turnID, DurationMS: duration.Milliseconds(),
		})
	case turnErr != nil:
		terminal = c.emit(sink, protocol.TypeTurnFailed, traceID, protocol.TurnFailedPayload{
			ProjectID: project.ID, ThreadID: threadID, TurnID: turnID, Code: "CODEX_TURN_FAILED", Message: turnErr.Error(), DurationMS: duration.Milliseconds(),
		})
	default:
		gitContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		gitResult, collectErr := c.collector.Collect(gitContext, workingDir)
		cancel()
		if collectErr != nil {
			terminal = c.emit(sink, protocol.TypeTurnFailed, traceID, protocol.TurnFailedPayload{
				ProjectID: project.ID, ThreadID: threadID, TurnID: turnID, Code: "RESULT_COLLECTION_FAILED", Message: collectErr.Error(), DurationMS: duration.Milliseconds(),
			})
		} else {
			terminal = c.emit(sink, protocol.TypeTurnCompleted, traceID, protocol.TurnCompletedPayload{
				ProjectID: project.ID, ThreadID: threadID, TurnID: turnID, DurationMS: turnResult.Duration.Milliseconds(), Summary: turnResult.Summary,
				ChangedFiles: gitResult.ChangedFiles, Diff: gitResult.Diff, DiffTruncated: gitResult.DiffTruncated,
			})
		}
	}

	c.mu.Lock()
	if c.traceID == traceID {
		c.traceID = ""
		c.projectID = ""
		c.threadID = ""
		c.turnID = ""
		c.startedAt = time.Time{}
		c.cancel = nil
		c.lastTerminal = &terminal
		c.liveItems = nil
		c.liveItemIndex = nil
	}
	c.mu.Unlock()
	c.emit(sink, protocol.TypeAgentStatus, traceID, protocol.AgentStatusPayload{Status: protocol.StatusIdle})
}

func (c *Controller) storeStartedItem(traceID string, item protocol.ThreadHistoryItem) int64 {
	if item.ID == "" {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.traceID != traceID {
		return 0
	}
	c.lastSequence++
	if index, ok := c.liveItemIndex[item.ID]; ok {
		c.liveItems[index] = item
	} else {
		c.liveItemIndex[item.ID] = len(c.liveItems)
		c.liveItems = append(c.liveItems, item)
	}
	return c.lastSequence
}

func (c *Controller) storeItemDelta(traceID string, event runner.Event) int64 {
	if event.ItemID == "" || event.Text == "" {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.traceID != traceID {
		return 0
	}
	index, ok := c.liveItemIndex[event.ItemID]
	if !ok {
		itemType := "activity"
		if event.Stream == "assistant" {
			itemType = "agentMessage"
		} else if event.Field == "output" {
			itemType = "commandExecution"
		}
		c.liveItemIndex[event.ItemID] = len(c.liveItems)
		c.liveItems = append(c.liveItems, protocol.ThreadHistoryItem{ID: event.ItemID, Type: itemType})
		index = len(c.liveItems) - 1
	}
	item := &c.liveItems[index]
	if event.Field == "output" {
		item.Output, item.Truncated = appendLiveDelta(item.Output, event.Text, item.Truncated)
	} else {
		item.Text, item.Truncated = appendLiveDelta(item.Text, event.Text, item.Truncated)
	}
	c.lastSequence++
	return c.lastSequence
}

func (c *Controller) storeCompletedItem(traceID string, item protocol.ThreadHistoryItem) int64 {
	if item.ID == "" {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.traceID != traceID {
		return 0
	}
	c.lastSequence++
	if index, ok := c.liveItemIndex[item.ID]; ok {
		c.liveItems[index] = item
	} else {
		c.liveItemIndex[item.ID] = len(c.liveItems)
		c.liveItems = append(c.liveItems, item)
	}
	return c.lastSequence
}

func appendLiveDelta(current, delta string, alreadyTruncated bool) (string, bool) {
	if alreadyTruncated {
		return current, true
	}
	value := current + delta
	if len(value) <= maxLiveItemFieldBytes {
		return value, false
	}
	const suffix = "..."
	end := maxLiveItemFieldBytes - len(suffix)
	for end > 0 && end < len(value) && (value[end]&0xc0) == 0x80 {
		end--
	}
	return value[:end] + suffix, true
}

func fitLiveSnapshot(items []protocol.ThreadHistoryItem) ([]protocol.ThreadHistoryItem, bool) {
	result := append([]protocol.ThreadHistoryItem(nil), items...)
	truncated := false
	for len(result) > 0 {
		data, err := json.Marshal(result)
		if err == nil && len(data) <= maxLiveSnapshotBytes {
			break
		}
		result = result[1:]
		truncated = true
	}
	return result, truncated
}

func (c *Controller) emit(sink EventSink, messageType, traceID string, payload any) protocol.Message {
	message, err := protocol.NewMessage(messageType, traceID, c.sender, payload)
	if err == nil && sink != nil {
		sink(message)
	}
	return message
}
