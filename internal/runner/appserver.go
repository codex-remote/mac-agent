package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ai-coding-remote/mac-agent/internal/codexapp"
)

const (
	EventStarted = "started"
	EventOutput  = "output"
)

var ErrTurnInterrupted = errors.New("Codex turn interrupted")

type AppServerClient interface {
	SetNotificationHandler(func(codexapp.Notification))
	ListThreads(context.Context, string) ([]codexapp.Thread, error)
	StartThread(context.Context, string) (codexapp.Thread, error)
	ResumeThread(context.Context, string, string) (codexapp.Thread, error)
	StartTurn(context.Context, string, string, string) (codexapp.Turn, error)
	InterruptTurn(context.Context, string, string) error
}

type AppServer struct {
	Client AppServerClient
}

type appServerEvent struct {
	kind     string
	threadID string
	turn     codexapp.Turn
	stream   string
	text     string
}

func (a AppServer) Run(ctx context.Context, request Request, emit func(Event)) (Result, error) {
	if a.Client == nil {
		return Result{}, fmt.Errorf("Codex app-server client is required")
	}
	events := make(chan appServerEvent, 512)
	terminal := make(chan appServerEvent, 1)
	a.Client.SetNotificationHandler(func(notification codexapp.Notification) {
		if event, ok := translateNotification(notification); ok {
			if event.kind == "completed" {
				select {
				case terminal <- event:
				default:
				}
				return
			}
			select {
			case events <- event:
			default:
			}
		}
	})
	defer a.Client.SetNotificationHandler(nil)

	thread, err := a.resolveThread(ctx, request)
	if err != nil {
		return Result{}, err
	}
	turn, err := a.Client.StartTurn(ctx, thread.ID, request.WorkingDir, request.Prompt)
	if err != nil {
		return Result{ThreadID: thread.ID}, err
	}
	startedAt := time.Now()
	if emit != nil {
		emit(Event{Kind: EventStarted, ThreadID: thread.ID, TurnID: turn.ID})
	}

	var summary strings.Builder
	for {
		select {
		case event := <-terminal:
			if event.threadID != thread.ID || event.turn.ID != turn.ID {
				continue
			}
			duration := time.Since(startedAt)
			if event.turn.DurationMS != nil {
				duration = time.Duration(*event.turn.DurationMS) * time.Millisecond
			}
			result := Result{ThreadID: thread.ID, TurnID: turn.ID, Duration: duration, Summary: strings.TrimSpace(summary.String())}
			switch event.turn.Status {
			case "completed":
				return result, nil
			case "interrupted":
				return result, ErrTurnInterrupted
			case "failed":
				message := "Codex turn failed"
				if event.turn.Error != nil && event.turn.Error.Message != "" {
					message = event.turn.Error.Message
				}
				return result, errors.New(message)
			default:
				return result, fmt.Errorf("unexpected Codex turn status %q", event.turn.Status)
			}
		case event := <-events:
			if event.threadID != thread.ID || (event.turn.ID != "" && event.turn.ID != turn.ID) {
				continue
			}
			switch event.kind {
			case EventOutput:
				if event.stream == "assistant" {
					summary.WriteString(event.text)
				}
				if emit != nil {
					emit(Event{Kind: EventOutput, ThreadID: thread.ID, TurnID: turn.ID, Stream: event.stream, Text: event.text})
				}
			}
		case <-ctx.Done():
			interruptContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = a.Client.InterruptTurn(interruptContext, thread.ID, turn.ID)
			cancel()
			return Result{ThreadID: thread.ID, TurnID: turn.ID, Duration: time.Since(startedAt), Summary: strings.TrimSpace(summary.String())}, ctx.Err()
		}
	}
}

func (a AppServer) resolveThread(ctx context.Context, request Request) (codexapp.Thread, error) {
	if request.ThreadID == "" {
		return a.Client.StartThread(ctx, request.WorkingDir)
	}
	threads, err := a.Client.ListThreads(ctx, request.WorkingDir)
	if err != nil {
		return codexapp.Thread{}, err
	}
	found := false
	for _, thread := range threads {
		if thread.ID == request.ThreadID {
			found = true
			break
		}
	}
	if !found {
		return codexapp.Thread{}, fmt.Errorf("thread %s does not belong to project %s", request.ThreadID, request.ProjectID)
	}
	return a.Client.ResumeThread(ctx, request.ThreadID, request.WorkingDir)
}

func translateNotification(notification codexapp.Notification) (appServerEvent, bool) {
	switch notification.Method {
	case "item/agentMessage/delta", "item/commandExecution/outputDelta":
		var payload struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			Delta    string `json:"delta"`
		}
		if json.Unmarshal(notification.Params, &payload) != nil {
			return appServerEvent{}, false
		}
		stream := "stdout"
		if notification.Method == "item/agentMessage/delta" {
			stream = "assistant"
		}
		return appServerEvent{kind: EventOutput, threadID: payload.ThreadID, turn: codexapp.Turn{ID: payload.TurnID}, stream: stream, text: payload.Delta}, true
	case "turn/completed":
		var payload struct {
			ThreadID string        `json:"threadId"`
			Turn     codexapp.Turn `json:"turn"`
		}
		if json.Unmarshal(notification.Params, &payload) != nil {
			return appServerEvent{}, false
		}
		return appServerEvent{kind: "completed", threadID: payload.ThreadID, turn: payload.Turn}, true
	default:
		return appServerEvent{}, false
	}
}
