package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jovemcleb/simple-orchestrator/internal/monitor"
	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
	"github.com/jovemcleb/simple-orchestrator/internal/providers/antigravity"
	"github.com/jovemcleb/simple-orchestrator/internal/providers/codex"
	"github.com/jovemcleb/simple-orchestrator/internal/providers/copilot"
	"github.com/jovemcleb/simple-orchestrator/internal/runtime"
	"github.com/jovemcleb/simple-orchestrator/internal/store"
)

func main() {
	os.Exit(runCLI(context.Background(), os.Args[1:], os.Stdout, os.Stderr, nil))
}

func runCLI(ctx context.Context, args []string, stdout, stderr io.Writer, customWorkers map[protocol.Provider]runtime.WorkerRunner) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printUsage(stdout)
		return 0
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "delegate":
		return runDelegate(ctx, rest, stdout, stderr, customWorkers)
	case "status":
		return runStatus(rest, stdout, stderr)
	case "ack":
		return runAck(rest, stdout, stderr)
	case "finish":
		return runFinish(rest, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "simple-orchestrator "+version)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown subcommand %q\n", sub)
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: simple-orchestrator <subcommand> [flags]

Subcommands:
  delegate   Start or reuse a run, execute a worker task synchronously, and output structured JSON event
  status     Inspect or recover a persisted run snapshot
  ack        Acknowledge receipt/processing of a worker event
  finish     Complete and close a run after all deliveries are acknowledged
  version    Print the version`)
}

// version is set at release build time with -ldflags "-X main.version=<tag>".
var version = "dev"

// terminalOpener opens the live terminal; tests replace it. noWatch is set by --no-watch.
var (
	terminalOpener      monitor.Opener = monitor.OpenTerminal
	noWatch             bool
	nativeWatch         bool
	copilotCLI          string
	experimentalCopilot bool
)

// disabledWorker rejects a provider that is present but not enabled.
type disabledWorker struct{ msg string }

func (d disabledWorker) RunTask(context.Context, protocol.CommandEnvelope) (runtime.WorkerResult, error) {
	return runtime.WorkerResult{Status: protocol.StatusFailed, Summary: d.msg}, errors.New(d.msg)
}

func buildEngine(stateDir, agyCLI, codexCLI, mcpConfig string, skipMCP bool, customWorkers map[protocol.Provider]runtime.WorkerRunner) (*runtime.Engine, *store.Store, error) {
	st, err := store.New(stateDir)
	if err != nil {
		return nil, nil, err
	}
	cx := codex.NewAdapter(codexCLI)
	native := nativeWatch && !noWatch
	cx.NativeWatch = native
	ag := antigravity.NewAdapter(agyCLI, mcpConfig, skipMCP)
	ag.NativeWatch = native
	cp := copilot.NewAdapter(copilotCLI)
	cp.NativeWatch = native
	var copilotWorker runtime.WorkerRunner = cp
	if !experimentalCopilot {
		copilotWorker = disabledWorker{msg: "o worker Copilot é experimental e está desligado; use --experimental-copilot (requer a CLI do Copilot logada numa conta com acesso a modelos explícitos)"}
	}
	workers := map[protocol.Provider]runtime.WorkerRunner{
		protocol.ProviderAntigravity: ag,
		protocol.ProviderCodex:       cx,
		protocol.ProviderCopilot:     copilotWorker,
	}
	for k, v := range customWorkers {
		workers[k] = v
	}
	eng := runtime.NewEngine(st, workers)
	if !noWatch {
		mon := monitor.New(st.BaseDir(), terminalOpener, nil)
		if native {
			mon.SetNative(string(protocol.ProviderCodex))
			mon.SetNative(string(protocol.ProviderAntigravity))
			mon.SetNative(string(protocol.ProviderCopilot))
		}
		eng.SetMonitor(mon)
	}
	return eng, st, nil
}

func runDelegate(ctx context.Context, args []string, stdout, stderr io.Writer, customWorkers map[protocol.Provider]runtime.WorkerRunner) int {
	fs := flag.NewFlagSet("delegate", flag.ContinueOnError)
	fs.SetOutput(stderr)

	stateDir := fs.String("state-dir", "", "Override state directory (default %LOCALAPPDATA%/SimpleOrchestrator)")
	runKey := fs.String("run-key", "", "Idempotency key for starting or reusing a run")
	runID := fs.String("run-id", "", "Explicit run ID")
	taskID := fs.String("task-id", "task-1", "Task ID")
	attemptID := fs.String("attempt-id", "att-1", "Attempt ID")
	maestraProvider := fs.String("maestra-provider", "", "Maestra provider (codex|antigravity|claude|copilot)")
	maestraSession := fs.String("maestra-session", "", "Explicit maestra session/conversation ID")
	workerProvider := fs.String("worker-provider", "", "Worker provider (codex|antigravity|claude|copilot)")
	workerSession := fs.String("worker-session", "", "Optional existing worker session ID for resume")
	workerProject := fs.String("worker-project", "", "Optional worker project name/ID when continuing in an existing chat or grouping a new chat")
	workerChat := fs.String("worker-chat", "", "Optional existing worker chat name/title to continue in (omit to start a new chat)")
	workerChatTitle := fs.String("worker-chat-title", "", "Optional display title when creating a new worker chat")
	workspace := fs.String("workspace", "", "Workspace directory path")
	target := fs.String("target", "", "Task target file or scope")
	change := fs.String("change", "", "Task change or inspection objective")
	constraints := fs.String("constraints", "Read-only; no product or database writes", "Task constraints")
	ownership := fs.String("ownership", "worker", "Task ownership")
	acceptance := fs.String("acceptance", "", "Observable acceptance criteria")
	prompt := fs.String("prompt", "", "Additional prompt instructions")
	model := fs.String("model", "default", "Requested worker model")
	effort := fs.String("effort", "", "Requested reasoning effort")
	timeoutSec := fs.Int("timeout", 0, "Optional worker timeout in seconds (0 = wait until turn completes)")
	readOnly := fs.Bool("read-only", false, "Run worker in read-only sandbox mode")
	skipMCP := fs.Bool("skip-headless-mcp", true, "Temporarily isolate global MCP config during headless Antigravity worker call")
	mcpConfig := fs.String("mcp-config", "", "Optional path to global mcp_config.json")
	agyCLI := fs.String("agy-cli", "", "Optional path to agy executable")
	codexCLI := fs.String("codex-cli", "", "Optional path to codex executable")
	fs.StringVar(&copilotCLI, "copilot-cli", "", "Experimental: optional path to the copilot executable (default: copilot on PATH)")
	fs.BoolVar(&experimentalCopilot, "experimental-copilot", false, "Experimental: enable the GitHub Copilot CLI worker (requires explicit --model and --effort)")
	autoAck := fs.Bool("auto-ack", false, "Automatically mark delivery acknowledged upon synchronous stdout return")
	fs.BoolVar(&noWatch, "no-watch", false, "Open no window at all: workers run headless with the structured stream (the live log is still written)")
	fs.BoolVar(&nativeWatch, "watch-native", true, "Open the worker's interactive CLI screen in its own window with the task as the first prompt; the bridge returns when that turn ends (validated from the CLI's session files) and the window stays open. Use --watch-native=false for the headless worker plus the read-only follow-along log terminal. Closing the window before the turn ends aborts the task")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	eng, _, err := buildEngine(*stateDir, *agyCLI, *codexCLI, *mcpConfig, *skipMCP, customWorkers)
	if err != nil {
		writeErrorJSON(stderr, err)
		return 1
	}

	cmd := protocol.CommandEnvelope{
		Action:           protocol.ActionDelegate,
		RunKey:           strings.TrimSpace(*runKey),
		RunID:            strings.TrimSpace(*runID),
		TaskID:           strings.TrimSpace(*taskID),
		AttemptID:        strings.TrimSpace(*attemptID),
		MaestraProvider:  protocol.Provider(strings.TrimSpace(*maestraProvider)),
		MaestraSessionID: strings.TrimSpace(*maestraSession),
		WorkerProvider:   protocol.Provider(strings.TrimSpace(*workerProvider)),
		WorkerSessionID:  strings.TrimSpace(*workerSession),
		WorkerProject:    strings.TrimSpace(*workerProject),
		WorkerChat:       strings.TrimSpace(*workerChat),
		WorkerChatTitle:  strings.TrimSpace(*workerChatTitle),
		Workspace:        strings.TrimSpace(*workspace),
		Profile: protocol.Profile{
			Provider:       protocol.Provider(strings.TrimSpace(*workerProvider)),
			Role:           protocol.RoleWorker,
			RequestedModel: strings.TrimSpace(*model),
			Effort:         strings.TrimSpace(*effort),
			TimeoutSeconds: *timeoutSec,
			MaxTurns:       1,
			ReadOnly:       *readOnly,
		},
		Spec: protocol.TaskSpec{
			Target:      strings.TrimSpace(*target),
			Change:      strings.TrimSpace(*change),
			Constraints: strings.TrimSpace(*constraints),
			Ownership:   strings.TrimSpace(*ownership),
			Acceptance:  strings.TrimSpace(*acceptance),
			Prompt:      strings.TrimSpace(*prompt),
		},
	}

	ev, duplicate, err := eng.Delegate(ctx, cmd)
	if err != nil {
		writeErrorJSON(stderr, err)
		return 1
	}
	if *autoAck && ev.Status == protocol.StatusSucceeded {
		_ = eng.Ack(ev.RunID, ev.TaskID, ev.AttemptID)
	}

	out := map[string]any{
		"ok":                          ev.Status == protocol.StatusSucceeded,
		"duplicate":                   duplicate,
		"inference_calls_during_wait": eng.InferenceCallsDuringWait(),
		"event":                       ev,
	}
	_ = writeJSON(stdout, out)
	if ev.Status != protocol.StatusSucceeded {
		return 1
	}
	return 0
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "Override state directory")
	runID := fs.String("run-id", "", "Run ID")
	recoverFlag := fs.Bool("recover", false, "Reconcile journal and outbox before returning status")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if strings.TrimSpace(*runID) == "" {
		writeErrorJSON(stderr, fmt.Errorf("--run-id is required"))
		return 2
	}
	st, err := store.New(*stateDir)
	if err != nil {
		writeErrorJSON(stderr, err)
		return 1
	}
	var snap *store.RunSnapshot
	if *recoverFlag {
		snap, err = st.RecoverRun(*runID)
	} else {
		snap, err = st.LoadSnapshot(*runID)
	}
	if err != nil {
		writeErrorJSON(stderr, err)
		return 1
	}
	_ = writeJSON(stdout, snap)
	return 0
}

func runAck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ack", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "Override state directory")
	runID := fs.String("run-id", "", "Run ID")
	taskID := fs.String("task-id", "", "Task ID")
	attemptID := fs.String("attempt-id", "", "Attempt ID")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if strings.TrimSpace(*runID) == "" || strings.TrimSpace(*taskID) == "" || strings.TrimSpace(*attemptID) == "" {
		writeErrorJSON(stderr, fmt.Errorf("--run-id, --task-id, and --attempt-id are required"))
		return 2
	}
	eng, st, err := buildEngine(*stateDir, "", "", "", false, nil)
	if err != nil {
		writeErrorJSON(stderr, err)
		return 1
	}
	if err := eng.Ack(*runID, *taskID, *attemptID); err != nil {
		writeErrorJSON(stderr, err)
		return 1
	}
	snap, err := st.LoadSnapshot(*runID)
	if err != nil {
		writeErrorJSON(stderr, err)
		return 1
	}
	_ = writeJSON(stdout, snap)
	return 0
}

func runFinish(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("finish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "Override state directory")
	runID := fs.String("run-id", "", "Run ID")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if strings.TrimSpace(*runID) == "" {
		writeErrorJSON(stderr, fmt.Errorf("--run-id is required"))
		return 2
	}
	eng, _, err := buildEngine(*stateDir, "", "", "", false, nil)
	if err != nil {
		writeErrorJSON(stderr, err)
		return 1
	}
	snap, err := eng.Finish(*runID)
	if err != nil {
		writeErrorJSON(stderr, err)
		return 1
	}
	_ = writeJSON(stdout, snap)
	return 0
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeErrorJSON(w io.Writer, err error) {
	_ = writeJSON(w, map[string]any{
		"ok":    false,
		"error": err.Error(),
	})
}
