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
	TypeAgentHello               = "agent.hello"
	TypeAgentStatus              = "agent.status"
	TypeAgentCapabilities        = "agent.capabilities"
	TypeExecutionProfileList     = "execution.profile.list"
	TypeExecutionProfileSnapshot = "execution.profile.snapshot"
	TypeProjectList              = "project.list"
	TypeProjectSnapshot          = "project.snapshot"
	TypeThreadList               = "thread.list"
	TypeThreadSnapshot           = "thread.snapshot"
	TypeThreadRead               = "thread.read"
	TypeThreadDetail             = "thread.detail"
	TypeTurnStart                = "turn.start"
	TypeTurnStarted              = "turn.started"
	TypeTurnOutput               = "turn.output"
	TypeTurnItemStarted          = "turn.item.started"
	TypeTurnItemDelta            = "turn.item.delta"
	TypeTurnItemDone             = "turn.item.completed"
	TypeTurnSnapshot             = "turn.snapshot"
	TypeTurnInterrupt            = "turn.interrupt"
	TypeTurnInterrupted          = "turn.interrupted"
	TypeTurnCompleted            = "turn.completed"
	TypeTurnFailed               = "turn.failed"
	TypeTurnRejected             = "turn.rejected"
	TypeTurnAcknowledged         = "turn.acknowledged"
	TypeRunAccepted              = "run.accepted"
	TypeRuntimeReceivedAck       = "runtime.received_ack"
	TypeRuntimeDurableAck        = "runtime.durable_ack"
	TypeBootstrapStart           = "bootstrap.start"
	TypeBootstrapBatch           = "bootstrap.batch"
	TypeBootstrapDurableAck      = "bootstrap.durable_ack"
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
	SpecVersion   string          `json:"spec_version"`
	MessageID     string          `json:"message_id"`
	Type          string          `json:"type"`
	OccurredAt    time.Time       `json:"occurred_at"`
	TraceID       string          `json:"trace_id"`
	Sender        Sender          `json:"sender"`
	Payload       json.RawMessage `json:"payload"`
	AgentSequence int64           `json:"agent_sequence,omitempty"`
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

type AgentCapabilitiesPayload struct {
	Restricted                 bool   `json:"restricted"`
	SandboxMode                string `json:"sandbox_mode"`
	ApprovalPolicy             string `json:"approval_policy"`
	WritableScope              string `json:"writable_scope"`
	NetworkAccess              bool   `json:"network_access"`
	CanRequestApproval         bool   `json:"can_request_approval"`
	HostProcessControl         bool   `json:"host_process_control"`
	UserLibraryWrite           bool   `json:"user_library_write"`
	XcodeDeviceControl         bool   `json:"xcode_device_control"`
	SupportsPermissionProfiles bool   `json:"supports_permission_profiles"`
}

type ExecutionProfileListPayload struct {
	ProjectID string `json:"project_id"`
}

type ExecutionProfile struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Allowed     bool   `json:"allowed"`
}

type ExecutionProfileSnapshotPayload struct {
	ProjectID        string             `json:"project_id"`
	DefaultProfileID string             `json:"default_profile_id"`
	Profiles         []ExecutionProfile `json:"profiles"`
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
	ID                   string    `json:"id"`
	ProjectID            string    `json:"project_id"`
	Title                string    `json:"title"`
	Preview              string    `json:"preview"`
	LatestMessagePreview string    `json:"latest_message_preview"`
	Status               string    `json:"status"`
	Source               string    `json:"source"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type ThreadSnapshotPayload struct {
	ProjectID string   `json:"project_id"`
	Threads   []Thread `json:"threads"`
}

type ThreadReadPayload struct {
	ProjectID string `json:"project_id"`
	ThreadID  string `json:"thread_id"`
}

type ThreadDetailPayload struct {
	ProjectID string       `json:"project_id"`
	Thread    ThreadDetail `json:"thread"`
	Truncated bool         `json:"truncated"`
}

type ThreadDetail struct {
	ID        string              `json:"id"`
	ProjectID string              `json:"project_id"`
	Title     string              `json:"title"`
	Preview   string              `json:"preview"`
	Status    string              `json:"status"`
	Source    string              `json:"source"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
	Turns     []ThreadHistoryTurn `json:"turns"`
}

type ThreadHistoryTurn struct {
	ID          string              `json:"id"`
	Status      string              `json:"status"`
	StartedAt   *time.Time          `json:"started_at,omitempty"`
	CompletedAt *time.Time          `json:"completed_at,omitempty"`
	DurationMS  *int64              `json:"duration_ms,omitempty"`
	Error       string              `json:"error,omitempty"`
	Items       []ThreadHistoryItem `json:"items"`
	Truncated   bool                `json:"truncated"`
}

type ThreadHistoryItem struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role,omitempty"`
	Phase      string             `json:"phase,omitempty"`
	Status     string             `json:"status,omitempty"`
	Text       string             `json:"text,omitempty"`
	Name       string             `json:"name,omitempty"`
	Command    string             `json:"command,omitempty"`
	CWD        string             `json:"cwd,omitempty"`
	Output     string             `json:"output,omitempty"`
	Path       string             `json:"path,omitempty"`
	Query      string             `json:"query,omitempty"`
	ExitCode   *int               `json:"exit_code,omitempty"`
	DurationMS *int64             `json:"duration_ms,omitempty"`
	Changes    []ThreadFileChange `json:"changes,omitempty"`
	Truncated  bool               `json:"truncated"`
}

type ThreadFileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Diff string `json:"diff,omitempty"`
}

type TurnStartPayload struct {
	CommandID           string `json:"command_id"`
	RunID               string `json:"run_id"`
	ProjectID           string `json:"project_id"`
	ThreadID            string `json:"thread_id,omitempty"`
	Prompt              string `json:"prompt"`
	PermissionProfileID string `json:"permission_profile_id,omitempty"`
}

type RunAcceptedPayload struct {
	CommandID string `json:"command_id"`
	RunID     string `json:"run_id"`
}

type RuntimeAckPayload struct {
	RunID         string `json:"run_id"`
	AgentSequence int64  `json:"agent_sequence"`
}

type BootstrapStartPayload struct {
	CommandID string `json:"command_id"`
	SyncID    string `json:"sync_id"`
}

type BootstrapBatchPayload struct {
	CommandID  string        `json:"command_id"`
	SyncID     string        `json:"sync_id"`
	SnapshotID string        `json:"snapshot_id"`
	BatchNo    int64         `json:"batch_no"`
	Checksum   string        `json:"checksum"`
	Project    *Project      `json:"project,omitempty"`
	Thread     *ThreadDetail `json:"thread,omitempty"`
	Done       bool          `json:"done"`
}

type BootstrapAckPayload struct {
	SyncID   string `json:"sync_id"`
	BatchNo  int64  `json:"batch_no"`
	Checksum string `json:"checksum"`
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

type TurnItemStartedPayload struct {
	ProjectID string            `json:"project_id"`
	ThreadID  string            `json:"thread_id"`
	TurnID    string            `json:"turn_id"`
	Sequence  int64             `json:"sequence"`
	Item      ThreadHistoryItem `json:"item"`
}

type TurnItemDeltaPayload struct {
	ProjectID string `json:"project_id"`
	ThreadID  string `json:"thread_id"`
	TurnID    string `json:"turn_id"`
	Sequence  int64  `json:"sequence"`
	ItemID    string `json:"item_id"`
	Field     string `json:"field"`
	Delta     string `json:"delta"`
}

type TurnItemCompletedPayload struct {
	ProjectID string            `json:"project_id"`
	ThreadID  string            `json:"thread_id"`
	TurnID    string            `json:"turn_id"`
	Sequence  int64             `json:"sequence"`
	Item      ThreadHistoryItem `json:"item"`
}

type TurnSnapshotPayload struct {
	ProjectID          string              `json:"project_id"`
	ThreadID           string              `json:"thread_id"`
	TurnID             string              `json:"turn_id"`
	Status             string              `json:"status"`
	StartedAt          time.Time           `json:"started_at"`
	RecentOutput       []string            `json:"recent_output"`
	LiveItems          []ThreadHistoryItem `json:"live_items,omitempty"`
	LastSequence       int64               `json:"last_sequence,omitempty"`
	LiveItemsTruncated bool                `json:"live_items_truncated,omitempty"`
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
	Code                 string                    `json:"code"`
	Message              string                    `json:"message"`
	RequiredCapabilities []string                  `json:"required_capabilities,omitempty"`
	RecoveryAction       string                    `json:"recovery_action,omitempty"`
	ExecutionContext     *AgentCapabilitiesPayload `json:"execution_context,omitempty"`
}

type TurnAcknowledgedPayload struct {
	TurnID string `json:"turn_id"`
	Status string `json:"status"`
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
