package protocol

import (
	"encoding/json"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	original, err := NewMessage(TypeTurnStart, "trace-1", Sender{Kind: "user", ID: "local-user"}, TurnStartPayload{
		ProjectID: "project-1",
		Prompt:    "fix the test",
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := PayloadAs[TurnStartPayload](decoded)
	if err != nil {
		t.Fatal(err)
	}
	if payload.ProjectID != "project-1" || payload.Prompt != "fix the test" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestDecodeRejectsUnsupportedVersion(t *testing.T) {
	message, err := NewMessage(TypeAgentStatus, "trace", Sender{Kind: "device", ID: "mac"}, AgentStatusPayload{Status: StatusIdle})
	if err != nil {
		t.Fatal(err)
	}
	message.SpecVersion = "1.0"
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err == nil {
		t.Fatal("expected unsupported version error")
	}
}
