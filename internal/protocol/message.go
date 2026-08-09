package protocol

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const SpecVersion = "1.0"

const (
	TypeAgentHello   = "agent.hello"
	TypeAgentStatus  = "agent.status"
	TypeRunStart     = "run.start"
	TypeRunStarted   = "run.started"
	TypeRunOutput    = "run.output"
	TypeRunSnapshot  = "run.snapshot"
	TypeRunCancel    = "run.cancel"
	TypeRunCancelled = "run.cancelled"
	TypeRunCompleted = "run.completed"
	TypeRunFailed    = "run.failed"
	TypeRunRejected  = "run.rejected"
)

const (
	StatusIdle      = "idle"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

type Sender struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Message struct {
	SpecVersion string          `json:"spec_version"`
	MessageID   string          `json:"message_id"`
	Type        string          `json:"type"`
	OccurredAt  time.Time       `json:"occurred_at"`
	TraceID     string          `json:"trace_id"`
	Sender      Sender          `json:"sender"`
	Payload     json.RawMessage `json:"payload"`
}

type AgentHelloPayload struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

type AgentStatusPayload struct {
	Status string `json:"status"`
	RunID  string `json:"run_id,omitempty"`
}

type RunStartPayload struct {
	RunID  string `json:"run_id"`
	TaskID string `json:"task_id,omitempty"`
	Prompt string `json:"prompt"`
}

type RunCancelPayload struct {
	RunID string `json:"run_id"`
}

type RunStartedPayload struct {
	RunID     string    `json:"run_id"`
	StartedAt time.Time `json:"started_at"`
}

type RunOutputPayload struct {
	RunID  string `json:"run_id"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

type RunSnapshotPayload struct {
	RunID        string    `json:"run_id,omitempty"`
	Status       string    `json:"status"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	RecentOutput []string  `json:"recent_output,omitempty"`
}

type RunCompletedPayload struct {
	RunID         string   `json:"run_id"`
	ExitCode      int      `json:"exit_code"`
	DurationMS    int64    `json:"duration_ms"`
	Summary       string   `json:"summary"`
	ChangedFiles  []string `json:"changed_files"`
	Diff          string   `json:"diff"`
	DiffTruncated bool     `json:"diff_truncated"`
}

type RunFailedPayload struct {
	RunID      string `json:"run_id"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	ExitCode   int    `json:"exit_code,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type RunCancelledPayload struct {
	RunID      string `json:"run_id"`
	DurationMS int64  `json:"duration_ms"`
}

type RunRejectedPayload struct {
	RunID   string `json:"run_id,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewMessage(messageType, traceID string, sender Sender, payload any) (Message, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Message{}, fmt.Errorf("marshal payload: %w", err)
	}
	if traceID == "" {
		traceID = NewID()
	}
	message := Message{
		SpecVersion: SpecVersion,
		MessageID:   NewID(),
		Type:        messageType,
		OccurredAt:  time.Now().UTC(),
		TraceID:     traceID,
		Sender:      sender,
		Payload:     data,
	}
	return message, message.Validate()
}

func Decode(data []byte) (Message, error) {
	var message Message
	if err := json.Unmarshal(data, &message); err != nil {
		return Message{}, fmt.Errorf("decode message: %w", err)
	}
	if err := message.Validate(); err != nil {
		return Message{}, err
	}
	return message, nil
}

func PayloadAs[T any](message Message) (T, error) {
	var payload T
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		return payload, fmt.Errorf("decode %s payload: %w", message.Type, err)
	}
	return payload, nil
}

func (m Message) Validate() error {
	switch {
	case m.SpecVersion != SpecVersion:
		return fmt.Errorf("unsupported spec_version %q", m.SpecVersion)
	case m.MessageID == "":
		return errors.New("message_id is required")
	case m.Type == "":
		return errors.New("type is required")
	case m.OccurredAt.IsZero():
		return errors.New("occurred_at is required")
	case m.TraceID == "":
		return errors.New("trace_id is required")
	case m.Sender.Kind == "" || m.Sender.ID == "":
		return errors.New("sender kind and id are required")
	case len(m.Payload) == 0:
		return errors.New("payload is required")
	default:
		return nil
	}
}

func NewID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(fmt.Sprintf("generate random ID: %v", err))
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return hex.EncodeToString(bytes[:])
}
