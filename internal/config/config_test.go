package config

import (
	"testing"
	"time"
)

func TestFromEnv(t *testing.T) {
	t.Setenv("AGENT_RELAY_URL", "wss://relay/ws/agent")
	t.Setenv("AGENT_WORKSPACE_ROOTS", "/tmp/work:/opt/projects")
	t.Setenv("AGENT_TURN_TIMEOUT", "5m")
	t.Setenv("AGENT_LOG_BUFFER_LINES", "42")
	t.Setenv("AGENT_PROJECT_SCAN_DEPTH", "3")
	config, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.TurnTimeout != 5*time.Minute || config.LogBufferLines != 42 || config.ProjectScanDepth != 3 || len(config.WorkspaceRoots) != 2 {
		t.Fatalf("unexpected config: %#v", config)
	}
}
