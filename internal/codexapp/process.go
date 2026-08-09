package codexapp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"
)

type Process struct {
	Client *Client
	cmd    *exec.Cmd
	cancel context.CancelFunc
	once   sync.Once
}

func StartProcess(parent context.Context, binary, version string, logger *slog.Logger) (*Process, error) {
	ctx, cancel := context.WithCancel(parent)
	command := exec.CommandContext(ctx, binary, "app-server", "--stdio")
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open Codex app-server stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open Codex app-server stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open Codex app-server stderr: %w", err)
	}
	if err := command.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start Codex app-server: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
	}()
	process := &Process{cmd: command, cancel: cancel}
	process.Client = NewClient(stdout, stdin, process)
	initializeContext, initializeCancel := context.WithTimeout(ctx, 10*time.Second)
	defer initializeCancel()
	if err := process.Client.Initialize(initializeContext, "ai-coding-remote-mac-agent", version); err != nil {
		_ = process.Close()
		return nil, err
	}
	logger.Info("Codex app-server started", "pid", command.Process.Pid)
	return process, nil
}

func (p *Process) Close() error {
	p.once.Do(func() {
		p.cancel()
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		_ = p.cmd.Wait()
	})
	return nil
}
