package codexapp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	resolvedBinary, err := resolveExecutable(binary)
	if err != nil {
		return nil, fmt.Errorf("resolve Codex executable: %w", err)
	}
	if err := validateAppBundle(resolvedBinary); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(parent)
	command := exec.CommandContext(ctx, resolvedBinary, "app-server", "--stdio")
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
	if err := process.Client.Initialize(initializeContext, "codex-remote-mac-agent", version); err != nil {
		_ = process.Close()
		return nil, err
	}
	logger.Info("Codex app-server started", "pid", command.Process.Pid, "binary", resolvedBinary)
	return process, nil
}

func resolveExecutable(binary string) (string, error) {
	candidate := strings.TrimSpace(binary)
	if candidate == "" {
		return "", fmt.Errorf("path is empty")
	}
	path, err := exec.LookPath(candidate)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("make path absolute: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks for %s: %w", absolute, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", resolved, err)
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%s is not executable", resolved)
	}
	return filepath.Clean(resolved), nil
}

func validateAppBundle(binary string) error {
	resourcesDirectory := filepath.Dir(binary)
	if filepath.Base(resourcesDirectory) != "Resources" || filepath.Base(filepath.Dir(resourcesDirectory)) != "Contents" {
		return nil
	}

	host := filepath.Join(resourcesDirectory, "codex-code-mode-host")
	info, err := os.Stat(host)
	if err != nil {
		return fmt.Errorf("Codex app bundle is incomplete: code mode host is unavailable at %s: %w", host, err)
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("Codex app bundle is incomplete: code mode host is not executable at %s", host)
	}
	return nil
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
