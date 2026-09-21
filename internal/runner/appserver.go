package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codex-remote/mac-agent/internal/codexapp"
	"github.com/codex-remote/mac-agent/internal/codexitem"
	"github.com/codex-remote/mac-agent/internal/protocol"
)

const (
	EventStarted       = "started"
	EventOutput        = "output"
	EventItemStarted   = "item_started"
	EventItemDelta     = "item_delta"
	EventItemCompleted = "item_completed"
	maxLiveFieldBytes  = 24 * 1024
)

var ErrTurnInterrupted = errors.New("Codex turn interrupted")

type AppServerClient interface {
	SetNotificationHandler(func(codexapp.Notification))
	ListThreads(context.Context, string) ([]codexapp.Thread, error)
	ListPermissionProfiles(context.Context, string) ([]codexapp.PermissionProfile, error)
	StartThread(context.Context, string, string) (codexapp.Thread, error)
	ResumeThread(context.Context, string, string, string) (codexapp.Thread, error)
	StartTurn(context.Context, string, string, string, string) (codexapp.Turn, error)
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
	itemID   string
	field    string
	item     protocol.ThreadHistoryItem
}

func (a AppServer) Run(ctx context.Context, request Request, emit func(Event)) (Result, error) {
	if a.Client == nil {
		return Result{}, fmt.Errorf("Codex app-server client is required")
	}
	if err := a.validatePermissionProfile(ctx, request.WorkingDir, request.PermissionProfileID); err != nil {
		return Result{}, err
	}
	events := make(chan appServerEvent, 512)
	a.Client.SetNotificationHandler(func(notification codexapp.Notification) {
		if event, ok := translateNotification(notification); ok {
			select {
			case events <- event:
			case <-ctx.Done():
			}
		}
	})
	defer a.Client.SetNotificationHandler(nil)

	thread, err := a.resolveThread(ctx, request)
	if err != nil {
		return Result{}, err
	}
	turn, err := a.Client.StartTurn(ctx, thread.ID, request.WorkingDir, request.Prompt, request.PermissionProfileID)
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
		case event := <-events:
			if event.threadID != thread.ID || event.turn.ID != turn.ID {
				continue
			}
			if event.kind == "completed" {
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
			}
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
			case EventItemStarted:
				if emit != nil {
					emit(Event{Kind: EventItemStarted, ThreadID: thread.ID, TurnID: turn.ID, Item: event.item})
				}
			case EventItemDelta:
				if event.stream == "assistant" {
					summary.WriteString(event.text)
				}
				if emit != nil {
					emit(Event{Kind: EventItemDelta, ThreadID: thread.ID, TurnID: turn.ID, Stream: event.stream, ItemID: event.itemID, Field: event.field, Text: event.text})
					if event.stream != "" {
						emit(Event{Kind: EventOutput, ThreadID: thread.ID, TurnID: turn.ID, Stream: event.stream, Text: event.text})
					}
				}
			case EventItemCompleted:
				if emit != nil {
					emit(Event{Kind: EventItemCompleted, ThreadID: thread.ID, TurnID: turn.ID, Item: event.item})
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

func (a AppServer) validatePermissionProfile(ctx context.Context, cwd, profileID string) error {
	if profileID == "" {
		return nil
	}
	profiles, err := a.Client.ListPermissionProfiles(ctx, cwd)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if profile.ID == profileID {
			if !profile.Allowed {
				return fmt.Errorf("permission profile %q is not allowed for this project", profileID)
			}
			return nil
		}
	}
	return fmt.Errorf("permission profile %q is not available for this project", profileID)
}

func (a AppServer) resolveThread(ctx context.Context, request Request) (codexapp.Thread, error) {
	if request.ThreadID == "" {
		return a.Client.StartThread(ctx, request.WorkingDir, request.PermissionProfileID)
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
	return a.Client.ResumeThread(ctx, request.ThreadID, request.WorkingDir, request.PermissionProfileID)
}

func translateNotification(notification codexapp.Notification) (appServerEvent, bool) {
	switch notification.Method {
	case "item/started", "item/completed":
		var payload struct {
			ThreadID string          `json:"threadId"`
			TurnID   string          `json:"turnId"`
			Item     json.RawMessage `json:"item"`
		}
		if json.Unmarshal(notification.Params, &payload) != nil {
			return appServerEvent{}, false
		}
		item, ok := codexitem.Normalize(payload.Item, maxLiveFieldBytes)
		if !ok {
			return appServerEvent{}, false
		}
		kind := EventItemStarted
		if notification.Method == "item/completed" {
			kind = EventItemCompleted
		}
		return appServerEvent{kind: kind, threadID: payload.ThreadID, turn: codexapp.Turn{ID: payload.TurnID}, item: item}, true
	case "item/agentMessage/delta", "item/commandExecution/outputDelta", "item/plan/delta",
		"item/reasoning/summaryTextDelta", "item/reasoning/textDelta", "item/mcpToolCall/progress":
		var payload struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			ItemID   string `json:"itemId"`
			Delta    string `json:"delta"`
			Message  string `json:"message"`
		}
		if json.Unmarshal(notification.Params, &payload) != nil {
			return appServerEvent{}, false
		}
		field := "text"
		stream := ""
		text := payload.Delta
		switch notification.Method {
		case "item/agentMessage/delta":
			stream = "assistant"
		case "item/commandExecution/outputDelta":
			field = "output"
			stream = "stdout"
		case "item/mcpToolCall/progress":
			text = payload.Message
		}
		if payload.ItemID == "" || text == "" {
			return appServerEvent{}, false
		}
		return appServerEvent{kind: EventItemDelta, threadID: payload.ThreadID, turn: codexapp.Turn{ID: payload.TurnID}, stream: stream, text: text, itemID: payload.ItemID, field: field}, true
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
