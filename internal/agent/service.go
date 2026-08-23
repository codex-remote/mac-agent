package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	turncontrol "github.com/ai-coding-remote/mac-agent/internal/turn"
)

type TurnController interface {
	Start(context.Context, string, protocol.TurnStartPayload, turncontrol.EventSink) error
	Interrupt(string, string) error
	Snapshot() turncontrol.Snapshot
}

type Inventory interface {
	Projects(context.Context) ([]protocol.Project, error)
	Threads(context.Context, string) ([]protocol.Thread, error)
	ReadThread(context.Context, string, string) (protocol.ThreadDetailPayload, error)
	ExecutionProfiles(context.Context, string) (protocol.ExecutionProfileSnapshotPayload, error)
}

type SourceReader interface {
	Read(context.Context, protocol.SourceReadPayload) (protocol.SourceSnapshotPayload, error)
}

type sourceReadError interface {
	error
	SourceCode() string
	PublicMessage() string
}

type Publisher func(protocol.Message) error

type DurableStore interface {
	PrepareRun(context.Context, string, string, any) (bool, error)
	AppendEvent(context.Context, string, protocol.Message) (protocol.Message, error)
	Pending(context.Context) ([]protocol.Message, error)
	DurableAck(context.Context, string, int64) error
	Cancel(context.Context, string) error
	PrepareBootstrap(context.Context, string, string) (string, int64, error)
	AckBootstrap(context.Context, string, int64, string, bool) error
}

type Service struct {
	context              context.Context
	name                 string
	version              string
	sender               protocol.Sender
	capabilities         protocol.AgentCapabilitiesPayload
	controller           TurnController
	inventory            Inventory
	sourceReader         SourceReader
	publish              Publisher
	durable              DurableStore
	bootstrapMu          sync.Mutex
	bootstrapAcks        map[string]chan protocol.BootstrapAckPayload
	bootstrapReadTimeout time.Duration
}

func (s *Service) SetDurableStore(store DurableStore)  { s.durable = store }
func (s *Service) SetSourceReader(reader SourceReader) { s.sourceReader = reader }

func (s *Service) StartDurableReplay(interval time.Duration) {
	if s.durable == nil || interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pending, err := s.durable.Pending(s.context)
				if err != nil {
					continue
				}
				for _, message := range pending {
					_ = s.publish(message)
				}
			case <-s.context.Done():
				return
			}
		}
	}()
}

func NewService(ctx context.Context, name, version string, sender protocol.Sender, capabilities protocol.AgentCapabilitiesPayload, controller TurnController, inventory Inventory, publish Publisher) *Service {
	return &Service{
		context: ctx, name: name, version: version, sender: sender, capabilities: capabilities,
		controller: controller, inventory: inventory, publish: publish,
		bootstrapAcks: make(map[string]chan protocol.BootstrapAckPayload), bootstrapReadTimeout: 20 * time.Second,
	}
}

func (s *Service) InitialMessages() []protocol.Message {
	snapshot := s.controller.Snapshot()
	hello := s.message(protocol.TypeAgentHello, "agent", protocol.AgentHelloPayload{Name: s.name, Version: s.version, Status: snapshot.Status})
	status := s.message(protocol.TypeAgentStatus, snapshot.TraceID, protocol.AgentStatusPayload{
		Status: snapshot.Status, ProjectID: snapshot.ProjectID, ThreadID: snapshot.ThreadID, TurnID: snapshot.TurnID,
	})
	capabilities := s.message(protocol.TypeAgentCapabilities, "agent", s.capabilities)
	messages := []protocol.Message{hello, status, capabilities}
	if s.durable != nil {
		if pending, err := s.durable.Pending(s.context); err == nil {
			messages = append(messages, pending...)
		}
	}
	if snapshot.Status == protocol.StatusRunning {
		messages = append(messages, s.message(protocol.TypeTurnSnapshot, snapshot.TraceID, protocol.TurnSnapshotPayload{
			ProjectID: snapshot.ProjectID, ThreadID: snapshot.ThreadID, TurnID: snapshot.TurnID,
			Status: snapshot.Status, StartedAt: snapshot.StartedAt, RecentOutput: snapshot.RecentOutput,
			LiveItems: snapshot.LiveItems, LastSequence: snapshot.LastSequence, LiveItemsTruncated: snapshot.LiveItemsTruncated,
		}))
	} else if snapshot.LastTerminal != nil {
		messages = append(messages, *snapshot.LastTerminal)
	}
	return messages
}

func (s *Service) Handle(_ context.Context, message protocol.Message) {
	switch message.Type {
	case protocol.TypeExecutionProfileList:
		payload, err := protocol.PayloadAs[protocol.ExecutionProfileListPayload](message)
		if err != nil || payload.ProjectID == "" {
			if err == nil {
				err = errors.New("project_id is required")
			}
			s.reject(message.TraceID, "MESSAGE_INVALID", err.Error())
			return
		}
		snapshot, err := s.inventory.ExecutionProfiles(s.context, payload.ProjectID)
		if err != nil {
			s.reject(message.TraceID, "EXECUTION_PROFILE_LIST_FAILED", err.Error())
			return
		}
		_ = s.publish(s.message(protocol.TypeExecutionProfileSnapshot, message.TraceID, snapshot))
	case protocol.TypeProjectList:
		projects, err := s.inventory.Projects(s.context)
		if err != nil {
			s.reject(message.TraceID, "PROJECT_LIST_FAILED", err.Error())
			return
		}
		_ = s.publish(s.message(protocol.TypeProjectSnapshot, message.TraceID, protocol.ProjectSnapshotPayload{Projects: projects}))
	case protocol.TypeThreadList:
		payload, err := protocol.PayloadAs[protocol.ThreadListPayload](message)
		if err != nil {
			s.reject(message.TraceID, "MESSAGE_INVALID", err.Error())
			return
		}
		threads, err := s.inventory.Threads(s.context, payload.ProjectID)
		if err != nil {
			s.reject(message.TraceID, "THREAD_LIST_FAILED", err.Error())
			return
		}
		_ = s.publish(s.message(protocol.TypeThreadSnapshot, message.TraceID, protocol.ThreadSnapshotPayload{ProjectID: payload.ProjectID, Threads: threads}))
	case protocol.TypeThreadRead:
		payload, err := protocol.PayloadAs[protocol.ThreadReadPayload](message)
		if err != nil || payload.ProjectID == "" || payload.ThreadID == "" {
			if err == nil {
				err = errors.New("project_id and thread_id are required")
			}
			s.reject(message.TraceID, "MESSAGE_INVALID", err.Error())
			return
		}
		detail, err := s.inventory.ReadThread(s.context, payload.ProjectID, payload.ThreadID)
		if err != nil {
			s.reject(message.TraceID, "THREAD_READ_FAILED", err.Error())
			return
		}
		_ = s.publish(s.message(protocol.TypeThreadDetail, message.TraceID, detail))
	case protocol.TypeSourceRead:
		payload, err := protocol.PayloadAs[protocol.SourceReadPayload](message)
		if err != nil || payload.ProjectID == "" || payload.Path == "" {
			s.publishSourceFailure(message.TraceID, "SOURCE_INVALID", "A project and source path are required.")
			return
		}
		if s.sourceReader == nil {
			s.publishSourceFailure(message.TraceID, "SOURCE_UNAVAILABLE", "Source reading is unavailable on this Mac Agent.")
			return
		}
		snapshot, err := s.sourceReader.Read(s.context, payload)
		if err != nil {
			var publicError sourceReadError
			if errors.As(err, &publicError) {
				s.publishSourceFailure(message.TraceID, publicError.SourceCode(), publicError.PublicMessage())
			} else {
				s.publishSourceFailure(message.TraceID, "SOURCE_READ_FAILED", "The source file could not be read.")
			}
			return
		}
		_ = s.publish(s.message(protocol.TypeSourceSnapshot, message.TraceID, snapshot))
	case protocol.TypeTurnStart:
		payload, err := protocol.PayloadAs[protocol.TurnStartPayload](message)
		if err != nil {
			s.reject(message.TraceID, "MESSAGE_INVALID", err.Error())
			return
		}
		runID := payload.RunID
		if runID == "" {
			runID = message.TraceID
		}
		if payload.CommandID == "" {
			payload.CommandID = message.MessageID
		}
		if s.durable != nil {
			created, prepareErr := s.durable.PrepareRun(s.context, runID, payload.CommandID, payload)
			if prepareErr != nil {
				s.reject(message.TraceID, "DURABLE_STORE_FAILED", prepareErr.Error())
				return
			}
			if !created {
				if pending, pendingErr := s.durable.Pending(s.context); pendingErr == nil {
					for _, event := range pending {
						_ = s.publish(event)
					}
				}
				return
			}
			accepted := s.message(protocol.TypeRunAccepted, runID, protocol.RunAcceptedPayload{CommandID: payload.CommandID, RunID: runID})
			if accepted, err = s.durable.AppendEvent(s.context, runID, accepted); err != nil {
				s.reject(message.TraceID, "DURABLE_STORE_FAILED", err.Error())
				return
			}
			_ = s.publish(accepted)
		}
		err = s.controller.Start(s.context, runID, payload, func(event protocol.Message) {
			if s.durable != nil && durableEvent(event.Type) {
				var persistErr error
				event, persistErr = s.durable.AppendEvent(s.context, runID, event)
				if persistErr != nil {
					return
				}
			}
			_ = s.publish(event)
		})
		switch {
		case errors.Is(err, turncontrol.ErrBusy):
			s.rejectRun(runID, "AGENT_BUSY", err.Error())
		case err != nil:
			s.rejectRun(runID, "TURN_START_FAILED", err.Error())
		}
	case protocol.TypeTurnInterrupt:
		payload, err := protocol.PayloadAs[protocol.TurnInterruptPayload](message)
		if err != nil {
			s.reject(message.TraceID, "MESSAGE_INVALID", err.Error())
			return
		}
		if s.durable != nil {
			_ = s.durable.Cancel(s.context, message.TraceID)
		}
		if err := s.controller.Interrupt(payload.ThreadID, payload.TurnID); err != nil {
			s.reject(message.TraceID, "TURN_NOT_FOUND", err.Error())
		}
	case protocol.TypeTurnAcknowledged:
		payload, err := protocol.PayloadAs[protocol.TurnAcknowledgedPayload](message)
		if err != nil || payload.TurnID == "" || !isTerminalStatus(payload.Status) {
			if err == nil {
				err = errors.New("turn_id and a terminal status are required")
			}
			s.reject(message.TraceID, "MESSAGE_INVALID", err.Error())
		}
	case protocol.TypeRuntimeReceivedAck:
		// Receipt only confirms the Redis copy. The local outbox is retained.
	case protocol.TypeRuntimeDurableAck:
		payload, err := protocol.PayloadAs[protocol.RuntimeAckPayload](message)
		if err != nil || payload.RunID == "" || payload.AgentSequence < 1 {
			s.reject(message.TraceID, "MESSAGE_INVALID", "run_id and agent_sequence are required")
			return
		}
		if s.durable != nil {
			if err := s.durable.DurableAck(s.context, payload.RunID, payload.AgentSequence); err != nil {
				s.reject(message.TraceID, "DURABLE_ACK_FAILED", err.Error())
			}
		}
	case protocol.TypeBootstrapStart:
		payload, err := protocol.PayloadAs[protocol.BootstrapStartPayload](message)
		if err != nil || payload.SyncID == "" || payload.CommandID == "" {
			s.reject(message.TraceID, "MESSAGE_INVALID", "sync_id and command_id are required")
			return
		}
		go s.runBootstrap(payload)
	case protocol.TypeBootstrapDurableAck:
		payload, err := protocol.PayloadAs[protocol.BootstrapAckPayload](message)
		if err != nil {
			return
		}
		s.bootstrapMu.Lock()
		waiter := s.bootstrapAcks[payload.SyncID]
		s.bootstrapMu.Unlock()
		if waiter != nil {
			select {
			case waiter <- payload:
			default:
			}
		}
	default:
		s.reject(message.TraceID, "MESSAGE_INVALID", fmt.Sprintf("unsupported message type %q", message.Type))
	}
}

func (s *Service) publishSourceFailure(traceID, code, message string) {
	_ = s.publish(s.message(protocol.TypeSourceReadFailed, traceID, protocol.SourceReadFailedPayload{Code: code, Message: message}))
}

func (s *Service) runBootstrap(command protocol.BootstrapStartPayload) {
	if s.durable == nil {
		return
	}
	snapshotID, last, err := s.durable.PrepareBootstrap(s.context, command.SyncID, command.CommandID)
	if err != nil {
		s.reject(command.SyncID, "BOOTSTRAP_STORE_FAILED", err.Error())
		return
	}
	waiter := make(chan protocol.BootstrapAckPayload, 1)
	s.bootstrapMu.Lock()
	if _, exists := s.bootstrapAcks[command.SyncID]; exists {
		s.bootstrapMu.Unlock()
		return
	}
	s.bootstrapAcks[command.SyncID] = waiter
	s.bootstrapMu.Unlock()
	defer func() {
		s.bootstrapMu.Lock()
		delete(s.bootstrapAcks, command.SyncID)
		s.bootstrapMu.Unlock()
	}()

	projects, totalSessions, err := s.loadBootstrapSnapshot()
	if err != nil {
		s.reject(command.SyncID, "BOOTSTRAP_INVENTORY_FAILED", err.Error())
		return
	}
	batchNo := int64(0)
	processedSessions := int64(0)
	for projectIndex := range projects {
		project := projects[projectIndex].project
		threads := projects[projectIndex].threads
		if len(threads) == 0 {
			batch := protocol.BootstrapBatchPayload{
				CommandID: command.CommandID, SyncID: command.SyncID, SnapshotID: snapshotID, BatchNo: batchNo,
				TotalSessions: totalSessions, ProcessedSessions: processedSessions, Project: &project,
			}
			if !s.sendBootstrapBatch(batch, last, waiter) {
				return
			}
			batchNo++
			continue
		}
		for _, thread := range threads {
			detail := s.readBootstrapThread(project.ID, thread)
			processedSessions++
			batch := protocol.BootstrapBatchPayload{
				CommandID: command.CommandID, SyncID: command.SyncID, SnapshotID: snapshotID, BatchNo: batchNo,
				TotalSessions: totalSessions, ProcessedSessions: processedSessions, Project: &project, Thread: &detail,
			}
			if !s.sendBootstrapBatch(batch, last, waiter) {
				return
			}
			batchNo++
		}
	}
	final := protocol.BootstrapBatchPayload{
		CommandID: command.CommandID, SyncID: command.SyncID, SnapshotID: snapshotID, BatchNo: batchNo,
		TotalSessions: totalSessions, ProcessedSessions: processedSessions,
		ReconciliationSafe: bootstrapReconciliationSafe(last), Done: true,
	}
	s.sendBootstrapBatch(final, last, waiter)
}

func bootstrapReconciliationSafe(lastDurableBatchNo int64) bool {
	return lastDurableBatchNo < 0
}

type bootstrapProjectSnapshot struct {
	project protocol.Project
	threads []protocol.Thread
}

func (s *Service) loadBootstrapSnapshot() ([]bootstrapProjectSnapshot, int64, error) {
	projects, err := s.inventory.Projects(s.context)
	if err != nil {
		return nil, 0, err
	}
	snapshot := make([]bootstrapProjectSnapshot, 0, len(projects))
	totalSessions := int64(0)
	for _, project := range projects {
		threads, err := s.inventory.Threads(s.context, project.ID)
		if err != nil {
			return nil, 0, err
		}
		totalSessions += int64(len(threads))
		snapshot = append(snapshot, bootstrapProjectSnapshot{project: project, threads: threads})
	}
	return snapshot, totalSessions, nil
}

func (s *Service) readBootstrapThread(projectID string, thread protocol.Thread) protocol.ThreadDetail {
	fallback := protocol.ThreadDetail{
		ID: thread.ID, ProjectID: projectID, Title: thread.Title, Preview: thread.Preview,
		Status: thread.Status, Source: thread.Source, CreatedAt: thread.UpdatedAt, UpdatedAt: thread.UpdatedAt,
		Turns: []protocol.ThreadHistoryTurn{},
	}
	ctx, cancel := context.WithTimeout(s.context, s.bootstrapReadTimeout)
	defer cancel()
	detail, err := s.inventory.ReadThread(ctx, projectID, thread.ID)
	if err != nil {
		return fallback
	}
	return detail.Thread
}

func (s *Service) sendBootstrapBatch(batch protocol.BootstrapBatchPayload, last int64, waiter <-chan protocol.BootstrapAckPayload) bool {
	checksumInput := batch
	checksumInput.Checksum = ""
	data, _ := json.Marshal(checksumInput)
	sum := sha256.Sum256(data)
	batch.Checksum = hex.EncodeToString(sum[:])
	if batch.BatchNo <= last {
		return true
	}
	message := s.message(protocol.TypeBootstrapBatch, batch.SyncID, batch)
	if err := s.publish(message); err != nil {
		return false
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case ack := <-waiter:
			if ack.BatchNo == batch.BatchNo && ack.Checksum == batch.Checksum {
				_ = s.durable.AckBootstrap(s.context, batch.SyncID, batch.BatchNo, batch.Checksum, batch.Done)
				return true
			}
		case <-timer.C:
			return false
		case <-s.context.Done():
			return false
		}
	}
}

func (s *Service) rejectRun(runID, code, text string) {
	message := s.message(protocol.TypeTurnRejected, runID, protocol.TurnRejectedPayload{Code: code, Message: text, ExecutionContext: &s.capabilities})
	if s.durable != nil {
		var err error
		message, err = s.durable.AppendEvent(s.context, runID, message)
		if err != nil {
			return
		}
	}
	_ = s.publish(message)
}

func durableEvent(messageType string) bool {
	switch messageType {
	case protocol.TypeTurnStarted, protocol.TypeTurnOutput, protocol.TypeTurnItemStarted, protocol.TypeTurnItemDelta,
		protocol.TypeTurnItemDone, protocol.TypeTurnCompleted, protocol.TypeTurnFailed, protocol.TypeTurnInterrupted:
		return true
	default:
		return false
	}
}

func isTerminalStatus(status string) bool {
	return status == "completed" || status == "failed" || status == "interrupted"
}

func (s *Service) reject(traceID, code, text string) {
	capabilities := s.capabilities
	_ = s.publish(s.message(protocol.TypeTurnRejected, traceID, protocol.TurnRejectedPayload{
		Code: code, Message: text, ExecutionContext: &capabilities,
	}))
}

func (s *Service) message(messageType, traceID string, payload any) protocol.Message {
	message, err := protocol.NewMessage(messageType, traceID, s.sender, payload)
	if err != nil {
		panic(err)
	}
	return message
}
