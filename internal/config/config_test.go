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
	t.Setenv("AGENT_CODEX_STATE_FILE", "/tmp/codex-state.json")
	config, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.TurnTimeout != 5*time.Minute || config.LogBufferLines != 42 || config.CodexStateFile != "/tmp/codex-state.json" || len(config.WorkspaceRoots) != 2 {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestFromEnvUsesCodexHomeForDesktopState(t *testing.T) {
	t.Setenv("AGENT_CODEX_STATE_FILE", "")
	t.Setenv("CODEX_HOME", "/tmp/custom-codex")

	got, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if got.CodexStateFile != "/tmp/custom-codex/.codex-global-state.json" {
		t.Fatalf("CodexStateFile = %q", got.CodexStateFile)
	}
}

func TestFromEnvUsesDefaultWorkspaceRoot(t *testing.T) {
	t.Setenv("AGENT_WORKSPACE_ROOTS", "")

	got, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.WorkspaceRoots) != 1 || got.WorkspaceRoots[0] != DefaultWorkspaceRoot {
		t.Fatalf("WorkspaceRoots = %#v, want [%q]", got.WorkspaceRoots, DefaultWorkspaceRoot)
	}
}
