package run

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
	ErrBusy        = errors.New("agent is already running")
	ErrRunNotFound = errors.New("run not found")
)

type GitCollector interface {
	Collect(context.Context, string) (result.GitResult, error)
}

type EventSink func(protocol.Message)

type Snapshot struct {
	RunID        string
	Status       string
	StartedAt    time.Time
	RecentOutput []string
	LastTerminal *protocol.Message
}

type Controller struct {
	mu           sync.RWMutex
	runner       runner.Runner
	workspace    workspace.Resolver
	collector    GitCollector
	timeout      time.Duration
	sender       protocol.Sender
	logs         *buffer.Ring
	currentID    string
	startedAt    time.Time
	cancel       context.CancelFunc
	lastTerminal *protocol.Message
}

func NewController(runRunner runner.Runner, resolver workspace.Resolver, collector GitCollector, timeout time.Duration, logLines int, sender protocol.Sender) *Controller {
	return &Controller{
		runner:    runRunner,
		workspace: resolver,
		collector: collector,
		timeout:   timeout,
		sender:    sender,
		logs:      buffer.New(logLines),
	}
}

func (c *Controller) Start(parent context.Context, runID, prompt string, sink EventSink) error {
	if runID == "" {
		return fmt.Errorf("run_id is required")
	}
	if prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	if len(prompt) > MaxPromptBytes {
		return fmt.Errorf("prompt exceeds %d bytes", MaxPromptBytes)
	}
	workingDir, err := c.workspace.Resolve(parent)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.currentID != "" {
		c.mu.Unlock()
		return ErrBusy
	}
	c.logs.Reset()
	startedAt := time.Now().UTC()
	var runContext context.Context
	var cancel context.CancelFunc
	if c.timeout > 0 {
		runContext, cancel = context.WithTimeout(parent, c.timeout)
	} else {
		runContext, cancel = context.WithCancel(parent)
	}
	c.currentID = runID
	c.startedAt = startedAt
	c.cancel = cancel
	c.lastTerminal = nil
	c.mu.Unlock()

	c.emit(sink, protocol.TypeRunStarted, runID, protocol.RunStartedPayload{RunID: runID, StartedAt: startedAt})
	c.emit(sink, protocol.TypeAgentStatus, runID, protocol.AgentStatusPayload{Status: protocol.StatusRunning, RunID: runID})
	go c.execute(runContext, runID, prompt, workingDir, startedAt, sink)
	return nil
}

func (c *Controller) Cancel(runID string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.currentID == "" || c.currentID != runID || c.cancel == nil {
		return ErrRunNotFound
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
	if c.currentID != "" {
		status = protocol.StatusRunning
	}
	snapshot := Snapshot{
		RunID:        c.currentID,
		Status:       status,
		StartedAt:    c.startedAt,
		RecentOutput: c.logs.Values(),
	}
	if c.lastTerminal != nil {
		terminal := *c.lastTerminal
		snapshot.LastTerminal = &terminal
	}
	return snapshot
}

func (c *Controller) execute(ctx context.Context, runID, prompt, workingDir string, startedAt time.Time, sink EventSink) {
	runnerResult, runErr := c.runner.Run(ctx, runner.Request{
		RunID:      runID,
		Prompt:     prompt,
		WorkingDir: workingDir,
	}, func(output runner.Output) {
		c.logs.Add(output.Text)
		c.emit(sink, protocol.TypeRunOutput, runID, protocol.RunOutputPayload{
			RunID:  runID,
			Stream: output.Stream,
			Text:   output.Text,
		})
	})
	duration := time.Since(startedAt)
	var terminal protocol.Message

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		terminal = c.emit(sink, protocol.TypeRunFailed, runID, protocol.RunFailedPayload{
			RunID: runID, Code: "RUN_TIMEOUT", Message: "Codex run timed out", ExitCode: runnerResult.ExitCode, DurationMS: duration.Milliseconds(),
		})
	case errors.Is(ctx.Err(), context.Canceled):
		terminal = c.emit(sink, protocol.TypeRunCancelled, runID, protocol.RunCancelledPayload{RunID: runID, DurationMS: duration.Milliseconds()})
	case runErr != nil:
		code := "CODEX_EXEC_FAILED"
		var startErr *runner.StartError
		if errors.As(runErr, &startErr) {
			code = "CODEX_START_FAILED"
		}
		terminal = c.emit(sink, protocol.TypeRunFailed, runID, protocol.RunFailedPayload{
			RunID: runID, Code: code, Message: runErr.Error(), ExitCode: runnerResult.ExitCode, DurationMS: duration.Milliseconds(),
		})
	default:
		gitContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		gitResult, collectErr := c.collector.Collect(gitContext, workingDir)
		cancel()
		if collectErr != nil {
			terminal = c.emit(sink, protocol.TypeRunFailed, runID, protocol.RunFailedPayload{
				RunID: runID, Code: "RESULT_COLLECTION_FAILED", Message: collectErr.Error(), ExitCode: runnerResult.ExitCode, DurationMS: duration.Milliseconds(),
			})
		} else {
			terminal = c.emit(sink, protocol.TypeRunCompleted, runID, protocol.RunCompletedPayload{
				RunID: runID, ExitCode: runnerResult.ExitCode, DurationMS: runnerResult.Duration.Milliseconds(), Summary: runnerResult.Summary,
				ChangedFiles: gitResult.ChangedFiles, Diff: gitResult.Diff, DiffTruncated: gitResult.DiffTruncated,
			})
		}
	}

	c.mu.Lock()
	if c.currentID == runID {
		c.currentID = ""
		c.startedAt = time.Time{}
		c.cancel = nil
		c.lastTerminal = &terminal
	}
	c.mu.Unlock()
	c.emit(sink, protocol.TypeAgentStatus, runID, protocol.AgentStatusPayload{Status: protocol.StatusIdle})
}

func (c *Controller) emit(sink EventSink, messageType, traceID string, payload any) protocol.Message {
	message, err := protocol.NewMessage(messageType, traceID, c.sender, payload)
	if err == nil && sink != nil {
		sink(message)
	}
	return message
}
