package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	runcontrol "github.com/ai-coding-remote/mac-agent/internal/run"
)

type RunController interface {
	Start(context.Context, string, string, runcontrol.EventSink) error
	Cancel(string) error
	Snapshot() runcontrol.Snapshot
}

type Publisher func(protocol.Message) error

type Service struct {
	context    context.Context
	name       string
	version    string
	sender     protocol.Sender
	controller RunController
	publish    Publisher
}

func NewService(ctx context.Context, name, version string, sender protocol.Sender, controller RunController, publish Publisher) *Service {
	return &Service{context: ctx, name: name, version: version, sender: sender, controller: controller, publish: publish}
}

func (s *Service) InitialMessages() []protocol.Message {
	snapshot := s.controller.Snapshot()
	hello := s.message(protocol.TypeAgentHello, "agent", protocol.AgentHelloPayload{Name: s.name, Version: s.version, Status: snapshot.Status})
	status := s.message(protocol.TypeAgentStatus, snapshot.RunID, protocol.AgentStatusPayload{Status: snapshot.Status, RunID: snapshot.RunID})
	messages := []protocol.Message{hello, status}
	if snapshot.Status == protocol.StatusRunning {
		messages = append(messages, s.message(protocol.TypeRunSnapshot, snapshot.RunID, protocol.RunSnapshotPayload{
			RunID: snapshot.RunID, Status: snapshot.Status, StartedAt: snapshot.StartedAt, RecentOutput: snapshot.RecentOutput,
		}))
	} else if snapshot.LastTerminal != nil {
		messages = append(messages, *snapshot.LastTerminal)
	}
	return messages
}

func (s *Service) Handle(_ context.Context, message protocol.Message) {
	switch message.Type {
	case protocol.TypeRunStart:
		payload, err := protocol.PayloadAs[protocol.RunStartPayload](message)
		if err != nil {
			s.reject(message.TraceID, "", "MESSAGE_INVALID", err.Error())
			return
		}
		err = s.controller.Start(s.context, payload.RunID, payload.Prompt, func(event protocol.Message) {
			_ = s.publish(event)
		})
		switch {
		case errors.Is(err, runcontrol.ErrBusy):
			s.reject(message.TraceID, payload.RunID, "AGENT_BUSY", err.Error())
		case err != nil:
			s.reject(message.TraceID, payload.RunID, "RUN_START_FAILED", err.Error())
		}
	case protocol.TypeRunCancel:
		payload, err := protocol.PayloadAs[protocol.RunCancelPayload](message)
		if err != nil {
			s.reject(message.TraceID, "", "MESSAGE_INVALID", err.Error())
			return
		}
		if err := s.controller.Cancel(payload.RunID); err != nil {
			s.reject(message.TraceID, payload.RunID, "RUN_NOT_FOUND", err.Error())
		}
	default:
		s.reject(message.TraceID, "", "MESSAGE_INVALID", fmt.Sprintf("unsupported message type %q", message.Type))
	}
}

func (s *Service) reject(traceID, runID, code, text string) {
	_ = s.publish(s.message(protocol.TypeRunRejected, traceID, protocol.RunRejectedPayload{RunID: runID, Code: code, Message: text}))
}

func (s *Service) message(messageType, traceID string, payload any) protocol.Message {
	message, err := protocol.NewMessage(messageType, traceID, s.sender, payload)
	if err != nil {
		panic(err)
	}
	return message
}
