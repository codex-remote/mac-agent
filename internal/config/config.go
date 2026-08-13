package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultWorkspaceRoot  = "/Users/leehooo/work"
	DefaultTurnTimeout    = 30 * time.Minute
	DefaultLogBufferLines = 500
	DefaultMaxDiffBytes   = 128 * 1024
)

type Config struct {
	RelayURL       string
	WorkspaceRoots []string
	CodexStateFile string
	CodexBinary    string
	AgentName      string
	TurnTimeout    time.Duration
	LogBufferLines int
	MaxDiffBytes   int
}

func FromEnv() (Config, error) {
	hostname, _ := os.Hostname()
	codexStateFile, err := resolveCodexStateFile()
	if err != nil {
		return Config{}, err
	}
	workspaceRoots := splitWorkspaceRoots(os.Getenv("AGENT_WORKSPACE_ROOTS"))
	if len(workspaceRoots) == 0 {
		workspaceRoots = []string{DefaultWorkspaceRoot}
	}
	config := Config{
		RelayURL:       os.Getenv("AGENT_RELAY_URL"),
		WorkspaceRoots: workspaceRoots,
		CodexStateFile: codexStateFile,
		CodexBinary:    envOrDefault("AGENT_CODEX_BINARY", "codex"),
		AgentName:      envOrDefault("AGENT_NAME", hostname),
		TurnTimeout:    DefaultTurnTimeout,
		LogBufferLines: DefaultLogBufferLines,
		MaxDiffBytes:   DefaultMaxDiffBytes,
	}
	if value := os.Getenv("AGENT_TURN_TIMEOUT"); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse AGENT_TURN_TIMEOUT: %w", err)
		}
		config.TurnTimeout = duration
	}
	if value := os.Getenv("AGENT_LOG_BUFFER_LINES"); value != "" {
		lines, err := strconv.Atoi(value)
		if err != nil || lines < 1 {
			return Config{}, fmt.Errorf("AGENT_LOG_BUFFER_LINES must be a positive integer")
		}
		config.LogBufferLines = lines
	}
	return config, nil
}

func (c Config) ValidateServe() error {
	if c.RelayURL == "" {
		return fmt.Errorf("Relay URL is required")
	}
	if c.CodexStateFile == "" {
		return fmt.Errorf("Codex Desktop state file is required")
	}
	return c.ValidateTurn()
}

func (c Config) ValidateTurn() error {
	if len(c.WorkspaceRoots) == 0 {
		return fmt.Errorf("at least one workspace root is required")
	}
	if c.CodexBinary == "" {
		return fmt.Errorf("Codex binary is required")
	}
	if c.TurnTimeout <= 0 {
		return fmt.Errorf("turn timeout must be positive")
	}
	if c.LogBufferLines < 1 {
		return fmt.Errorf("log buffer lines must be positive")
	}
	return nil
}

func splitWorkspaceRoots(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := filepath.SplitList(value)
	roots := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			roots = append(roots, value)
		}
	}
	return roots
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func resolveCodexStateFile() (string, error) {
	if value := strings.TrimSpace(os.Getenv("AGENT_CODEX_STATE_FILE")); value != "" {
		return value, nil
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home for Codex Desktop state: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}
	return filepath.Join(codexHome, ".codex-global-state.json"), nil
}
