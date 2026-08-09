package agent

import (
	"context"
	"errors"
	"fmt"

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
}

type Publisher func(protocol.Message) error

type Service struct {
	context    context.Context
	name       string
	version    string
	sender     protocol.Sender
	controller TurnController
	inventory  Inventory
	publish    Publisher
}

func NewService(ctx context.Context, name, version string, sender protocol.Sender, controller TurnController, inventory Inventory, publish Publisher) *Service {
	return &Service{context: ctx, name: name, version: version, sender: sender, controller: controller, inventory: inventory, publish: publish}
}

func (s *Service) InitialMessages() []protocol.Message {
	snapshot := s.controller.Snapshot()
	hello := s.message(protocol.TypeAgentHello, "agent", protocol.AgentHelloPayload{Name: s.name, Version: s.version, Status: snapshot.Status})
	status := s.message(protocol.TypeAgentStatus, snapshot.TraceID, protocol.AgentStatusPayload{
		Status: snapshot.Status, ProjectID: snapshot.ProjectID, ThreadID: snapshot.ThreadID, TurnID: snapshot.TurnID,
	})
	messages := []protocol.Message{hello, status}
	if snapshot.Status == protocol.StatusRunning {
		messages = append(messages, s.message(protocol.TypeTurnSnapshot, snapshot.TraceID, protocol.TurnSnapshotPayload{
			ProjectID: snapshot.ProjectID, ThreadID: snapshot.ThreadID, TurnID: snapshot.TurnID,
			Status: snapshot.Status, StartedAt: snapshot.StartedAt, RecentOutput: snapshot.RecentOutput,
		}))
	} else if snapshot.LastTerminal != nil {
		messages = append(messages, *snapshot.LastTerminal)
	}
	return messages
}

func (s *Service) Handle(_ context.Context, message protocol.Message) {
	switch message.Type {
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
	case protocol.TypeTurnStart:
		payload, err := protocol.PayloadAs[protocol.TurnStartPayload](message)
		if err != nil {
			s.reject(message.TraceID, "MESSAGE_INVALID", err.Error())
			return
		}
		err = s.controller.Start(s.context, message.TraceID, payload, func(event protocol.Message) {
			_ = s.publish(event)
		})
		switch {
		case errors.Is(err, turncontrol.ErrBusy):
			s.reject(message.TraceID, "AGENT_BUSY", err.Error())
		case err != nil:
			s.reject(message.TraceID, "TURN_START_FAILED", err.Error())
		}
	case protocol.TypeTurnInterrupt:
		payload, err := protocol.PayloadAs[protocol.TurnInterruptPayload](message)
		if err != nil {
			s.reject(message.TraceID, "MESSAGE_INVALID", err.Error())
			return
		}
		if err := s.controller.Interrupt(payload.ThreadID, payload.TurnID); err != nil {
			s.reject(message.TraceID, "TURN_NOT_FOUND", err.Error())
		}
	default:
		s.reject(message.TraceID, "MESSAGE_INVALID", fmt.Sprintf("unsupported message type %q", message.Type))
	}
}

func (s *Service) reject(traceID, code, text string) {
	_ = s.publish(s.message(protocol.TypeTurnRejected, traceID, protocol.TurnRejectedPayload{Code: code, Message: text}))
}

func (s *Service) message(messageType, traceID string, payload any) protocol.Message {
	message, err := protocol.NewMessage(messageType, traceID, s.sender, payload)
	if err != nil {
		panic(err)
	}
	return message
}
