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
	"github.com/ai-coding-remote/mac-agent/internal/codexapp"
	"github.com/ai-coding-remote/mac-agent/internal/config"
	"github.com/ai-coding-remote/mac-agent/internal/durable"
	"github.com/ai-coding-remote/mac-agent/internal/inventory"
	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	"github.com/ai-coding-remote/mac-agent/internal/relay"
	"github.com/ai-coding-remote/mac-agent/internal/result"
	"github.com/ai-coding-remote/mac-agent/internal/runner"
	turncontrol "github.com/ai-coding-remote/mac-agent/internal/turn"
	"github.com/ai-coding-remote/mac-agent/internal/workspace"
)

var version = "0.0.1"

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
	case "turn":
		return turnLocal(arguments[1:])
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
	workspaceRoots := stringListFlag{values: append([]string(nil), base.WorkspaceRoots...)}
	flags.Var(&workspaceRoots, "workspace-root", "allowed root for Codex Desktop projects (repeatable)")
	codexStateFile := flags.String("codex-state-file", base.CodexStateFile, "Codex Desktop global state JSON file")
	codexBinary := flags.String("codex-binary", base.CodexBinary, "Codex CLI executable")
	agentName := flags.String("name", base.AgentName, "Agent display name")
	timeout := flags.Duration("timeout", base.TurnTimeout, "maximum duration of one turn")
	logLines := flags.Int("log-buffer-lines", base.LogBufferLines, "recent output lines retained in memory")
	runtimeDBPath := flags.String("runtime-db", base.RuntimeDBPath, "durable Runtime SQLite path")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	base.RelayURL = *relayURL
	base.WorkspaceRoots = workspaceRoots.values
	base.CodexStateFile = *codexStateFile
	base.CodexBinary = *codexBinary
	base.AgentName = *agentName
	base.TurnTimeout = *timeout
	base.LogBufferLines = *logLines
	base.RuntimeDBPath = *runtimeDBPath
	if err := base.ValidateServe(); err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		return 2
	}
	catalog, err := workspace.NewCodexCatalog(base.WorkspaceRoots, base.CodexStateFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "workspace error:", err)
		return 2
	}
	projects, err := catalog.List(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "workspace error:", err)
		return 2
	}
	if len(projects) == 0 {
		fmt.Fprintln(os.Stderr, "workspace error: no Codex Desktop projects found within workspace roots")
		return 2
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	appServer, err := codexapp.StartProcess(ctx, base.CodexBinary, version, logger)
	if err != nil {
		logger.Error("Start Codex app-server", "error", err)
		return 1
	}
	defer appServer.Close()
	sender := protocol.Sender{Kind: "device", ID: "local-mac"}
	controller := turncontrol.NewController(
		runner.AppServer{Client: appServer.Client}, catalog, result.Collector{MaxDiffBytes: base.MaxDiffBytes},
		base.TurnTimeout, base.LogBufferLines, sender,
	)
	defer controller.Close()
	projectInventory := inventory.New(catalog, appServer.Client)
	client := relay.New(relay.Config{URL: base.RelayURL}, logger)
	capabilities := protocol.AgentCapabilitiesPayload{
		Restricted: true, SandboxMode: codexapp.RemoteSandboxMode, ApprovalPolicy: codexapp.RemoteApprovalPolicy,
		WritableScope: "selected_project", NetworkAccess: false, CanRequestApproval: false,
		HostProcessControl: false, UserLibraryWrite: false, XcodeDeviceControl: false,
		SupportsPermissionProfiles: true,
	}
	service := agent.NewService(ctx, base.AgentName, version, sender, capabilities, controller, projectInventory, client.Publish)
	durableStore, err := durable.Open(base.RuntimeDBPath)
	if err != nil {
		logger.Error("Open durable Runtime SQLite", "path", base.RuntimeDBPath, "error", err)
		return 1
	}
	defer durableStore.Close()
	service.SetDurableStore(durableStore)
	service.StartDurableReplay(2 * time.Second)
	if err := client.Run(ctx, service.InitialMessages, service.Handle); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("Agent stopped", "error", err)
		return 1
	}
	return 0
}

func turnLocal(arguments []string) int {
	base, err := config.FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		return 2
	}
	flags := flag.NewFlagSet("turn", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	projectDir := flags.String("project-dir", "", "Git project directory")
	codexBinary := flags.String("codex-binary", base.CodexBinary, "Codex CLI executable")
	prompt := flags.String("prompt", "", "development instruction")
	timeout := flags.Duration("timeout", base.TurnTimeout, "maximum turn duration")
	logLines := flags.Int("log-buffer-lines", base.LogBufferLines, "recent output lines retained in memory")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if *prompt == "" && flags.NArg() > 0 {
		*prompt = strings.Join(flags.Args(), " ")
	}
	base.WorkspaceRoots = []string{*projectDir}
	base.CodexBinary = *codexBinary
	base.TurnTimeout = *timeout
	base.LogBufferLines = *logLines
	if err := base.ValidateTurn(); err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		return 2
	}
	if strings.TrimSpace(*prompt) == "" {
		fmt.Fprintln(os.Stderr, "configuration error: prompt is required")
		return 2
	}
	catalog, err := workspace.NewGitProjectCatalog(*projectDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "workspace error:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	appServer, err := codexapp.StartProcess(ctx, base.CodexBinary, version, logger)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Codex app-server error:", err)
		return 1
	}
	defer appServer.Close()
	projects, err := catalog.List(ctx)
	if err != nil || len(projects) != 1 {
		fmt.Fprintln(os.Stderr, "project error: could not load project-dir")
		return 2
	}
	sender := protocol.Sender{Kind: "device", ID: "local-mac"}
	controller := turncontrol.NewController(
		runner.AppServer{Client: appServer.Client}, catalog, result.Collector{MaxDiffBytes: base.MaxDiffBytes},
		base.TurnTimeout, base.LogBufferLines, sender,
	)
	defer controller.Close()
	traceID := protocol.NewID()
	events := make(chan protocol.Message, 256)
	if err := controller.Start(ctx, traceID, protocol.TurnStartPayload{ProjectID: projects[0].ID, Prompt: *prompt}, func(message protocol.Message) { events <- message }); err != nil {
		fmt.Fprintln(os.Stderr, "start error:", err)
		return 1
	}
	for {
		message := <-events
		terminal, exitCode := printLocalEvent(message)
		if terminal {
			return exitCode
		}
	}
}

func printLocalEvent(message protocol.Message) (bool, int) {
	switch message.Type {
	case protocol.TypeTurnStarted:
		payload, _ := protocol.PayloadAs[protocol.TurnStartedPayload](message)
		fmt.Fprintf(os.Stderr, "[mac-agent] turn started: thread=%s turn=%s\n", payload.ThreadID, payload.TurnID)
	case protocol.TypeTurnOutput:
		payload, err := protocol.PayloadAs[protocol.TurnOutputPayload](message)
		if err != nil {
			fmt.Fprintln(os.Stderr, "decode output:", err)
			return false, 0
		}
		if payload.Stream == "stderr" {
			fmt.Fprint(os.Stderr, payload.Text)
		} else {
			fmt.Fprint(os.Stdout, payload.Text)
		}
	case protocol.TypeTurnCompleted:
		payload, err := protocol.PayloadAs[protocol.TurnCompletedPayload](message)
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
	case protocol.TypeTurnFailed:
		payload, _ := protocol.PayloadAs[protocol.TurnFailedPayload](message)
		data, _ := json.Marshal(payload)
		fmt.Fprintf(os.Stderr, "\n[mac-agent] turn failed: %s\n", data)
		return true, 1
	case protocol.TypeTurnInterrupted:
		fmt.Fprintln(os.Stderr, "\n[mac-agent] turn interrupted")
		return true, 130
	}
	return false, 0
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `AI Coding Remote Mac Agent

Usage:
  mac-agent serve [flags]
  mac-agent turn --project-dir DIR --prompt TEXT [flags]
  mac-agent version

Commands:
  serve    Connect to Relay and control Codex projects and turns
  turn     Execute one local Codex turn without Relay
  version  Print the build version`)
}

type stringListFlag struct {
	values   []string
	explicit bool
}

func (f *stringListFlag) String() string { return strings.Join(f.values, ",") }
func (f *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("workspace root cannot be empty")
	}
	if !f.explicit {
		f.values = nil
		f.explicit = true
	}
	f.values = append(f.values, value)
	return nil
}
