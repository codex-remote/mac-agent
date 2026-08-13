package runner

import (
	"context"
	"time"

	"github.com/ai-coding-remote/mac-agent/internal/protocol"
)

type Request struct {
	ProjectID           string
	ThreadID            string
	Prompt              string
	WorkingDir          string
	PermissionProfileID string
}

type Event struct {
	Kind     string
	ThreadID string
	TurnID   string
	Stream   string
	Text     string
	ItemID   string
	Field    string
	Item     protocol.ThreadHistoryItem
}

type Result struct {
	ThreadID string
	TurnID   string
	Duration time.Duration
	Summary  string
}

type Runner interface {
	Run(context.Context, Request, func(Event)) (Result, error)
}
