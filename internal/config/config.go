package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	DefaultRunTimeout     = 30 * time.Minute
	DefaultLogBufferLines = 500
	DefaultMaxDiffBytes   = 128 * 1024
)

type Config struct {
	RelayURL       string
	WorkingDir     string
	CodexBinary    string
	AgentName      string
	RunTimeout     time.Duration
	LogBufferLines int
	MaxDiffBytes   int
}

func FromEnv() (Config, error) {
	hostname, _ := os.Hostname()
	config := Config{
		RelayURL:       os.Getenv("AGENT_RELAY_URL"),
		WorkingDir:     os.Getenv("AGENT_WORKING_DIR"),
		CodexBinary:    envOrDefault("AGENT_CODEX_BINARY", "codex"),
		AgentName:      envOrDefault("AGENT_NAME", hostname),
		RunTimeout:     DefaultRunTimeout,
		LogBufferLines: DefaultLogBufferLines,
		MaxDiffBytes:   DefaultMaxDiffBytes,
	}
	if value := os.Getenv("AGENT_RUN_TIMEOUT"); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse AGENT_RUN_TIMEOUT: %w", err)
		}
		config.RunTimeout = duration
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
	return c.ValidateRun()
}

func (c Config) ValidateRun() error {
	if c.WorkingDir == "" {
		return fmt.Errorf("working directory is required")
	}
	if c.CodexBinary == "" {
		return fmt.Errorf("Codex binary is required")
	}
	if c.RunTimeout <= 0 {
		return fmt.Errorf("run timeout must be positive")
	}
	if c.LogBufferLines < 1 {
		return fmt.Errorf("log buffer lines must be positive")
	}
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
