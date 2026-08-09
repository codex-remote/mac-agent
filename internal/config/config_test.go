package config

import (
	"testing"
	"time"
)

func TestFromEnv(t *testing.T) {
	t.Setenv("AGENT_RELAY_URL", "wss://relay/ws/agent")
	t.Setenv("AGENT_WORKING_DIR", "/tmp/repo")
	t.Setenv("AGENT_RUN_TIMEOUT", "5m")
	t.Setenv("AGENT_LOG_BUFFER_LINES", "42")
	config, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.RunTimeout != 5*time.Minute || config.LogBufferLines != 42 {
		t.Fatalf("unexpected config: %#v", config)
	}
}
