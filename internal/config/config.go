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
	DefaultTurnTimeout      = 30 * time.Minute
	DefaultLogBufferLines   = 500
	DefaultMaxDiffBytes     = 128 * 1024
	DefaultProjectScanDepth = 4
)

type Config struct {
	RelayURL         string
	WorkspaceRoots   []string
	ProjectScanDepth int
	CodexBinary      string
	AgentName        string
	TurnTimeout      time.Duration
	LogBufferLines   int
	MaxDiffBytes     int
}

func FromEnv() (Config, error) {
	hostname, _ := os.Hostname()
	config := Config{
		RelayURL:         os.Getenv("AGENT_RELAY_URL"),
		WorkspaceRoots:   splitWorkspaceRoots(os.Getenv("AGENT_WORKSPACE_ROOTS")),
		ProjectScanDepth: DefaultProjectScanDepth,
		CodexBinary:      envOrDefault("AGENT_CODEX_BINARY", "codex"),
		AgentName:        envOrDefault("AGENT_NAME", hostname),
		TurnTimeout:      DefaultTurnTimeout,
		LogBufferLines:   DefaultLogBufferLines,
		MaxDiffBytes:     DefaultMaxDiffBytes,
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
	if value := os.Getenv("AGENT_PROJECT_SCAN_DEPTH"); value != "" {
		depth, err := strconv.Atoi(value)
		if err != nil || depth < 1 {
			return Config{}, fmt.Errorf("AGENT_PROJECT_SCAN_DEPTH must be a positive integer")
		}
		config.ProjectScanDepth = depth
	}
	return config, nil
}

func (c Config) ValidateServe() error {
	if c.RelayURL == "" {
		return fmt.Errorf("Relay URL is required")
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
