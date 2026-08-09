package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultBinary         = "codex"
	defaultSandbox        = "workspace-write"
	maxOutputLineBytes    = 1024 * 1024
	maxSummaryBytes       = 16 * 1024
	processShutdownPeriod = 2 * time.Second
)

type Codex struct {
	Binary  string
	Sandbox string
}

func (c Codex) Run(ctx context.Context, request Request, emit func(Output)) (Result, error) {
	binary := c.Binary
	if binary == "" {
		binary = defaultBinary
	}
	sandbox := c.Sandbox
	if sandbox == "" {
		sandbox = defaultSandbox
	}
	args := []string{
		"exec",
		"--ephemeral",
		"--sandbox", sandbox,
		"--cd", request.WorkingDir,
		"--color", "never",
		request.Prompt,
	}
	command := exec.Command(binary, args...)
	command.Dir = request.WorkingDir
	command.Env = safeEnvironment()
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := command.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("open Codex stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return Result{}, fmt.Errorf("open Codex stderr: %w", err)
	}
	startedAt := time.Now()
	if err := command.Start(); err != nil {
		return Result{}, &StartError{Err: fmt.Errorf("start Codex: %w", err)}
	}

	var summary strings.Builder
	var scanWG sync.WaitGroup
	scanErrors := make(chan error, 2)
	scanWG.Add(2)
	go scanOutput(stdout, "stdout", emit, &summary, &scanWG, scanErrors)
	go scanOutput(stderr, "stderr", emit, nil, &scanWG, scanErrors)

	waitResult := make(chan error, 1)
	go func() {
		waitResult <- command.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitResult:
	case <-ctx.Done():
		terminateProcessGroup(command.Process.Pid, syscall.SIGTERM)
		select {
		case waitErr = <-waitResult:
		case <-time.After(processShutdownPeriod):
			terminateProcessGroup(command.Process.Pid, syscall.SIGKILL)
			waitErr = <-waitResult
		}
	}
	scanWG.Wait()
	close(scanErrors)
	for scanErr := range scanErrors {
		if scanErr != nil && waitErr == nil {
			waitErr = scanErr
		}
	}
	duration := time.Since(startedAt)
	exitCode := command.ProcessState.ExitCode()
	result := Result{
		ExitCode: exitCode,
		Duration: duration,
		Summary:  truncateString(strings.TrimSpace(summary.String()), maxSummaryBytes),
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if waitErr != nil {
		return result, &ExitError{Code: exitCode, Err: waitErr}
	}
	return result, nil
}

func scanOutput(reader io.Reader, stream string, emit func(Output), summary *strings.Builder, waitGroup *sync.WaitGroup, errorsChannel chan<- error) {
	defer waitGroup.Done()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxOutputLineBytes)
	for scanner.Scan() {
		text := scanner.Text() + "\n"
		if summary != nil && summary.Len() < maxSummaryBytes {
			remaining := maxSummaryBytes - summary.Len()
			if len(text) > remaining {
				textForSummary := text[:remaining]
				summary.WriteString(textForSummary)
			} else {
				summary.WriteString(text)
			}
		}
		if emit != nil {
			emit(Output{Stream: stream, Text: text})
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		errorsChannel <- fmt.Errorf("scan Codex %s: %w", stream, err)
	}
}

func terminateProcessGroup(pid int, signal syscall.Signal) {
	if pid > 0 {
		_ = syscall.Kill(-pid, signal)
	}
}

func safeEnvironment() []string {
	allowed := map[string]struct{}{
		"CODEX_API_KEY": {}, "CODEX_HOME": {}, "HOME": {}, "HTTPS_PROXY": {},
		"HTTP_PROXY": {}, "LANG": {}, "LC_ALL": {}, "NO_PROXY": {}, "PATH": {},
		"SHELL": {}, "TERM": {}, "TMPDIR": {}, "USER": {},
		httpProxyLower(): {}, httpsProxyLower(): {}, noProxyLower(): {},
	}
	environment := make([]string, 0, len(allowed))
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, ok := allowed[name]; ok || strings.HasPrefix(name, "LC_") {
			environment = append(environment, entry)
		}
	}
	return environment
}

func httpProxyLower() string  { return "http_proxy" }
func httpsProxyLower() string { return "https_proxy" }
func noProxyLower() string    { return "no_proxy" }

func truncateString(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	return strings.ToValidUTF8(value[:maxBytes], "�")
}
