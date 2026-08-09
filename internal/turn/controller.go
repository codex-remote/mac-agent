package turn

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ai-coding-remote/mac-agent/internal/buffer"
	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	"github.com/ai-coding-remote/mac-agent/internal/result"
	"github.com/ai-coding-remote/mac-agent/internal/runner"
	"github.com/ai-coding-remote/mac-agent/internal/workspace"
)

const MaxPromptBytes = 16 * 1024

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
	TraceID      string
	ProjectID    string
	ThreadID     string
	TurnID       string
	Status       string
	StartedAt    time.Time
	RecentOutput []string
	LastTerminal *protocol.Message
}

type Controller struct {
	mu           sync.RWMutex
	runner       runner.Runner
	catalog      ProjectCatalog
	collector    GitCollector
	timeout      time.Duration
	sender       protocol.Sender
	logs         *buffer.Ring
	traceID      string
	projectID    string
	threadID     string
	turnID       string
	startedAt    time.Time
	cancel       context.CancelFunc
	lastTerminal *protocol.Message
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
	snapshot := Snapshot{
		TraceID: c.traceID, ProjectID: c.projectID, ThreadID: c.threadID, TurnID: c.turnID,
		Status: status, StartedAt: c.startedAt, RecentOutput: c.logs.Values(),
	}
	if c.lastTerminal != nil {
		terminal := *c.lastTerminal
		snapshot.LastTerminal = &terminal
	}
	return snapshot
}

func (c *Controller) execute(ctx context.Context, traceID string, project workspace.Project, payload protocol.TurnStartPayload, startedAt time.Time, sink EventSink) {
	turnResult, turnErr := c.runner.Run(ctx, runner.Request{
		ProjectID: project.ID, ThreadID: payload.ThreadID, Prompt: payload.Prompt, WorkingDir: project.Path,
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
		gitResult, collectErr := c.collector.Collect(gitContext, project.Path)
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
	}
	c.mu.Unlock()
	c.emit(sink, protocol.TypeAgentStatus, traceID, protocol.AgentStatusPayload{Status: protocol.StatusIdle})
}

func (c *Controller) emit(sink EventSink, messageType, traceID string, payload any) protocol.Message {
	message, err := protocol.NewMessage(messageType, traceID, c.sender, payload)
	if err == nil && sink != nil {
		sink(message)
	}
	return message
}
