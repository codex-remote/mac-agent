package protocol

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const SpecVersion = "2.0"

const (
	TypeAgentHello      = "agent.hello"
	TypeAgentStatus     = "agent.status"
	TypeProjectList     = "project.list"
	TypeProjectSnapshot = "project.snapshot"
	TypeThreadList      = "thread.list"
	TypeThreadSnapshot  = "thread.snapshot"
	TypeTurnStart       = "turn.start"
	TypeTurnStarted     = "turn.started"
	TypeTurnOutput      = "turn.output"
	TypeTurnSnapshot    = "turn.snapshot"
	TypeTurnInterrupt   = "turn.interrupt"
	TypeTurnInterrupted = "turn.interrupted"
	TypeTurnCompleted   = "turn.completed"
	TypeTurnFailed      = "turn.failed"
	TypeTurnRejected    = "turn.rejected"
)

const (
	StatusIdle        = "idle"
	StatusRunning     = "running"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"
	StatusInterrupted = "interrupted"
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
	Status    string `json:"status"`
	ProjectID string `json:"project_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
}

type ProjectListPayload struct{}

type Project struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Path        string     `json:"path"`
	ThreadCount int        `json:"thread_count"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

type ProjectSnapshotPayload struct {
	Projects []Project `json:"projects"`
}

type ThreadListPayload struct {
	ProjectID string `json:"project_id"`
}

type Thread struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Title     string    `json:"title"`
	Preview   string    `json:"preview"`
	Status    string    `json:"status"`
	Source    string    `json:"source"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ThreadSnapshotPayload struct {
	ProjectID string   `json:"project_id"`
	Threads   []Thread `json:"threads"`
}

type TurnStartPayload struct {
	ProjectID string `json:"project_id"`
	ThreadID  string `json:"thread_id,omitempty"`
	Prompt    string `json:"prompt"`
}

type TurnStartedPayload struct {
	ProjectID string    `json:"project_id"`
	ThreadID  string    `json:"thread_id"`
	TurnID    string    `json:"turn_id"`
	StartedAt time.Time `json:"started_at"`
}

type TurnOutputPayload struct {
	ProjectID string `json:"project_id"`
	ThreadID  string `json:"thread_id"`
	TurnID    string `json:"turn_id"`
	Stream    string `json:"stream"`
	Text      string `json:"text"`
}

type TurnSnapshotPayload struct {
	ProjectID    string    `json:"project_id"`
	ThreadID     string    `json:"thread_id"`
	TurnID       string    `json:"turn_id"`
	Status       string    `json:"status"`
	StartedAt    time.Time `json:"started_at"`
	RecentOutput []string  `json:"recent_output"`
}

type TurnInterruptPayload struct {
	ThreadID string `json:"thread_id"`
	TurnID   string `json:"turn_id"`
}

type TurnInterruptedPayload struct {
	ProjectID  string `json:"project_id"`
	ThreadID   string `json:"thread_id"`
	TurnID     string `json:"turn_id"`
	DurationMS int64  `json:"duration_ms"`
}

type TurnCompletedPayload struct {
	ProjectID     string   `json:"project_id"`
	ThreadID      string   `json:"thread_id"`
	TurnID        string   `json:"turn_id"`
	DurationMS    int64    `json:"duration_ms"`
	Summary       string   `json:"summary"`
	ChangedFiles  []string `json:"changed_files"`
	Diff          string   `json:"diff"`
	DiffTruncated bool     `json:"diff_truncated"`
}

type TurnFailedPayload struct {
	ProjectID  string `json:"project_id,omitempty"`
	ThreadID   string `json:"thread_id,omitempty"`
	TurnID     string `json:"turn_id,omitempty"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type TurnRejectedPayload struct {
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
