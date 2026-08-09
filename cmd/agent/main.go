package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ai-coding-remote/mac-agent/internal/agent"
	"github.com/ai-coding-remote/mac-agent/internal/config"
	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	"github.com/ai-coding-remote/mac-agent/internal/relay"
	"github.com/ai-coding-remote/mac-agent/internal/result"
	"github.com/ai-coding-remote/mac-agent/internal/run"
	"github.com/ai-coding-remote/mac-agent/internal/runner"
	"github.com/ai-coding-remote/mac-agent/internal/workspace"
)

var version = "dev"

func main() {
	os.Exit(realMain(os.Args[1:]))
}

func realMain(arguments []string) int {
	if len(arguments) == 0 {
		printUsage()
		return 2
	}
	switch arguments[0] {
	case "serve":
		return serve(arguments[1:])
	case "run":
		return runLocal(arguments[1:])
	case "version", "--version", "-version":
		fmt.Println(version)
		return 0
	case "help", "--help", "-h":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", arguments[0])
		printUsage()
		return 2
	}
}

func serve(arguments []string) int {
	base, err := config.FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		return 2
	}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	relayURL := flags.String("relay-url", base.RelayURL, "Relay WebSocket URL")
	workingDir := flags.String("working-dir", base.WorkingDir, "fixed Git working directory")
	codexBinary := flags.String("codex-binary", base.CodexBinary, "Codex CLI executable")
	agentName := flags.String("name", base.AgentName, "Agent display name")
	timeout := flags.Duration("timeout", base.RunTimeout, "maximum duration of one run")
	logLines := flags.Int("log-buffer-lines", base.LogBufferLines, "recent output lines retained in memory")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	base.RelayURL = *relayURL
	base.WorkingDir = *workingDir
	base.CodexBinary = *codexBinary
	base.AgentName = *agentName
	base.RunTimeout = *timeout
	base.LogBufferLines = *logLines
	if err := base.ValidateServe(); err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		return 2
	}
	resolver, err := workspace.NewFixed(base.WorkingDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "workspace error:", err)
		return 2
	}
	if _, err := resolver.Resolve(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "workspace error:", err)
		return 2
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sender := protocol.Sender{Kind: "device", ID: "local-mac"}
	controller := run.NewController(
		runner.Codex{Binary: base.CodexBinary}, resolver, result.Collector{MaxDiffBytes: base.MaxDiffBytes},
		base.RunTimeout, base.LogBufferLines, sender,
	)
	defer controller.Close()
	client := relay.New(relay.Config{URL: base.RelayURL}, logger)
	service := agent.NewService(ctx, base.AgentName, version, sender, controller, client.Publish)
	if err := client.Run(ctx, service.InitialMessages, service.Handle); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("Agent stopped", "error", err)
		return 1
	}
	return 0
}

func runLocal(arguments []string) int {
	base, err := config.FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		return 2
	}
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	workingDir := flags.String("working-dir", base.WorkingDir, "fixed Git working directory")
	codexBinary := flags.String("codex-binary", base.CodexBinary, "Codex CLI executable")
	prompt := flags.String("prompt", "", "development instruction")
	timeout := flags.Duration("timeout", base.RunTimeout, "maximum run duration")
	logLines := flags.Int("log-buffer-lines", base.LogBufferLines, "recent output lines retained in memory")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if *prompt == "" && flags.NArg() > 0 {
		*prompt = strings.Join(flags.Args(), " ")
	}
	base.WorkingDir = *workingDir
	base.CodexBinary = *codexBinary
	base.RunTimeout = *timeout
	base.LogBufferLines = *logLines
	if err := base.ValidateRun(); err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		return 2
	}
	if strings.TrimSpace(*prompt) == "" {
		fmt.Fprintln(os.Stderr, "configuration error: prompt is required")
		return 2
	}
	resolver, err := workspace.NewFixed(base.WorkingDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "workspace error:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sender := protocol.Sender{Kind: "device", ID: "local-mac"}
	controller := run.NewController(
		runner.Codex{Binary: base.CodexBinary}, resolver, result.Collector{MaxDiffBytes: base.MaxDiffBytes},
		base.RunTimeout, base.LogBufferLines, sender,
	)
	defer controller.Close()
	runID := protocol.NewID()
	events := make(chan protocol.Message, 256)
	if err := controller.Start(ctx, runID, *prompt, func(message protocol.Message) { events <- message }); err != nil {
		fmt.Fprintln(os.Stderr, "start error:", err)
		return 1
	}
	interrupt := ctx.Done()
	for {
		select {
		case message := <-events:
			terminal, exitCode := printLocalEvent(message)
			if terminal {
				return exitCode
			}
		case <-interrupt:
			_ = controller.Cancel(runID)
			fmt.Fprintln(os.Stderr, "[mac-agent] stopping run...")
			interrupt = nil
		}
	}
}

func printLocalEvent(message protocol.Message) (bool, int) {
	switch message.Type {
	case protocol.TypeRunStarted:
		fmt.Fprintln(os.Stderr, "[mac-agent] run started")
	case protocol.TypeRunOutput:
		payload, err := protocol.PayloadAs[protocol.RunOutputPayload](message)
		if err != nil {
			fmt.Fprintln(os.Stderr, "decode output:", err)
			return false, 0
		}
		if payload.Stream == "stderr" {
			fmt.Fprint(os.Stderr, payload.Text)
		} else {
			fmt.Fprint(os.Stdout, payload.Text)
		}
	case protocol.TypeRunCompleted:
		payload, err := protocol.PayloadAs[protocol.RunCompletedPayload](message)
		if err != nil {
			fmt.Fprintln(os.Stderr, "decode completion:", err)
			return true, 1
		}
		fmt.Fprintf(os.Stderr, "\n[mac-agent] completed in %s; changed files: %d\n", time.Duration(payload.DurationMS)*time.Millisecond, len(payload.ChangedFiles))
		if payload.Summary != "" {
			fmt.Fprintf(os.Stdout, "\nFinal summary:\n%s\n", payload.Summary)
		}
		if len(payload.ChangedFiles) > 0 {
			fmt.Fprintf(os.Stdout, "\nChanged files:\n- %s\n", strings.Join(payload.ChangedFiles, "\n- "))
		}
		if payload.Diff != "" {
			fmt.Fprintf(os.Stdout, "\nGit diff:\n%s\n", payload.Diff)
		}
		return true, 0
	case protocol.TypeRunFailed:
		payload, _ := protocol.PayloadAs[protocol.RunFailedPayload](message)
		data, _ := json.Marshal(payload)
		fmt.Fprintf(os.Stderr, "\n[mac-agent] run failed: %s\n", data)
		return true, 1
	case protocol.TypeRunCancelled:
		fmt.Fprintln(os.Stderr, "\n[mac-agent] run cancelled")
		return true, 130
	}
	return false, 0
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `AI Coding Remote Mac Agent

Usage:
  mac-agent serve [flags]
  mac-agent run --working-dir DIR --prompt TEXT [flags]
  mac-agent version

Commands:
  serve    Connect to Relay and execute incoming Run messages
  run      Execute one local Codex Run without Relay
  version  Print the build version`)
}
