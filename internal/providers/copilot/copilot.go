// Package copilot runs GitHub Copilot CLI workers. The model and reasoning
// effort must be explicit, and a silent switch by the CLI (for example to
// "auto" when the model is unavailable) fails the task instead of being
// accepted (R04).
package copilot

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jovemcleb/simple-orchestrator/internal/monitor"
	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
	"github.com/jovemcleb/simple-orchestrator/internal/providers/codex"
	"github.com/jovemcleb/simple-orchestrator/internal/runtime"
)

var loaderEntry = []string{"@github", "copilot", "npm-loader.js"}

type Adapter struct {
	CLIPath string
	// NativeWatch opens the interactive Copilot screen in its own window
	// (--watch-native) instead of the headless JSONL stream.
	NativeWatch  bool
	Launch       codex.NativeLauncher
	StateDir     string // defaults to ~/.copilot/session-state
	PollInterval time.Duration
}

func NewAdapter(cliPath string) *Adapter {
	if strings.TrimSpace(cliPath) == "" {
		if p, err := exec.LookPath("copilot"); err == nil {
			cliPath = p
		} else {
			cliPath = "copilot"
		}
	}
	return &Adapter{CLIPath: cliPath}
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// requireModelAndEffort enforces an explicit model and effort: "auto" lets the
// CLI pick the model and does not accept an effort, so it is not allowed.
func requireModelAndEffort(cmd protocol.CommandEnvelope) (string, string, error) {
	model, effort := protocol.ResolveModelAndEffort(protocol.ProviderCopilot, cmd.Profile.RequestedModel, cmd.Profile.Effort)
	switch strings.ToLower(model) {
	case "", "default", "auto":
		return "", "", errors.New("Copilot exige --model explícito (ex.: gpt-5.6-luna, claude-sonnet-5.5); 'auto' e o padrão não são aceitos")
	}
	if effort == "" {
		return "", "", errors.New("Copilot exige --effort explícito (none|minimal|low|medium|high|xhigh|max)")
	}
	return model, effort, nil
}

func (a *Adapter) stateDir() string {
	if a.StateDir != "" {
		return a.StateDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".copilot", "session-state")
}

// buildArgs returns the CLI flags shared by both modes, without the prompt flag.
func buildArgs(cmd protocol.CommandEnvelope, model, effort, sessionID string, resume bool) []string {
	args := []string{"--model", model, "--reasoning-effort", effort}
	if resume {
		args = append(args, "--resume", sessionID)
	} else {
		args = append(args, "--session-id", sessionID, "-n", cmd.EffectiveChatTitle())
	}
	args = append(args, "--allow-all-tools", "--no-auto-update", "--no-ask-user")
	if cmd.Profile.ReadOnly {
		args = append(args, "--deny-tool", "write")
	}
	return args
}

func (a *Adapter) RunTask(ctx context.Context, cmd protocol.CommandEnvelope) (runtime.WorkerResult, error) {
	live := monitor.FromContext(ctx)
	fail := func(msg string) (runtime.WorkerResult, error) {
		live.Line("FALHA: " + msg)
		return runtime.WorkerResult{WorkerSessionID: cmd.WorkerSessionID, ObservedModel: "unknown", Status: protocol.StatusFailed, Summary: msg}, errors.New(msg)
	}
	if strings.TrimSpace(cmd.WorkerChat) != "" {
		return fail("Copilot: continuar por nome de chat ainda não é suportado; use --worker-session com o ID da sessão")
	}
	model, effort, err := requireModelAndEffort(cmd)
	if err != nil {
		return fail(err.Error())
	}

	runCtx := ctx
	if cmd.Profile.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(cmd.Profile.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	cli := cmd.Profile.CLIPath
	if strings.TrimSpace(cli) == "" {
		cli = a.CLIPath
	}
	argv0, err := codex.ResolveNodeCLI(cli, loaderEntry...)
	if err != nil {
		return fail(err.Error())
	}

	sid := strings.TrimSpace(cmd.WorkerSessionID)
	resume := sid != ""
	if !resume {
		sid = newUUID()
	}
	live.Line(fmt.Sprintf("Sessão Copilot: %s | modelo %s | esforço %s", sid, model, effort))
	prompt := codex.BuildPrompt(cmd.Spec)
	base := buildArgs(cmd, model, effort, sid, resume)
	start := time.Now()

	var res runtime.WorkerResult
	if a.NativeWatch {
		res, err = a.runNative(runCtx, cmd, append(append(argv0, base...), "-i", prompt), sid, model, start)
	} else {
		res, err = a.runHeadless(runCtx, cmd, append(append(argv0, base...), "-p", prompt, "--output-format", "json", "--stream", "off"), sid, model, start)
	}
	if err == nil {
		live.Line("Turno concluído.")
	}
	return res, err
}

// runHeadless runs `copilot -p --output-format json` and evaluates the stream.
func (a *Adapter) runHeadless(ctx context.Context, cmd protocol.CommandEnvelope, argv []string, sid, model string, start time.Time) (runtime.WorkerResult, error) {
	live := monitor.FromContext(ctx)
	proc := exec.CommandContext(ctx, argv[0], argv[1:]...)
	proc.Dir = cmd.Workspace
	proc.Env = append(os.Environ(), "CI=true")
	var stderr bytes.Buffer
	proc.Stderr = &stderr
	out, err := proc.StdoutPipe()
	if err != nil {
		return failResult(live, sid, err.Error())
	}
	if err := proc.Start(); err != nil {
		return failResult(live, sid, err.Error())
	}
	tr := newTracker(model, time.Time{})
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		live.Raw(line)
		tr.feed(line)
	}
	waitErr := proc.Wait()
	if tr.failure == "" && waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		tr.failure = msg
	}
	return tr.result(sid)
}

// runNative opens the interactive Copilot screen and follows the session's
// events.jsonl until the delegated turn ends; the window stays open afterwards.
func (a *Adapter) runNative(ctx context.Context, cmd protocol.CommandEnvelope, argv []string, sid, model string, start time.Time) (runtime.WorkerResult, error) {
	live := monitor.FromContext(ctx)
	launch := a.Launch
	if launch == nil {
		launch = codex.DefaultNativeLauncher
	}
	exited, kill, err := launch(cmd.Workspace, argv)
	if err != nil {
		return failResult(live, sid, "não foi possível abrir a janela interativa do Copilot: "+err.Error())
	}
	live.Line("Janela interativa do Copilot aberta; aguardando o primeiro turno terminar.")

	events := filepath.Join(a.stateDir(), sid, "events.jsonl")
	poll := a.PollInterval
	if poll <= 0 {
		poll = 700 * time.Millisecond
	}
	var tr *tracker
	check := func() bool {
		b, err := os.ReadFile(events)
		if err != nil {
			return false
		}
		tr = newTracker(model, start)
		for _, line := range bytes.Split(b, []byte("\n")) {
			tr.feed(line)
		}
		return tr.decided()
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			kill()
			return failResult(live, sid, "execução cancelada ou expirada antes do turno terminar; janela do Copilot encerrada: "+ctx.Err().Error())
		case <-exited:
			if check() {
				return tr.result(sid)
			}
			return failResult(live, sid, "a janela do Copilot foi fechada antes do turno terminar; a tarefa foi interrompida")
		case <-ticker.C:
			if check() {
				if tr.modelSwitched {
					// The CLI keeps going with another model; stop it so the
					// substitute model does not consume the task.
					kill()
					live.Line("Janela do Copilot encerrada: o modelo pedido não foi usado.")
				}
				return tr.result(sid)
			}
		}
	}
}

func failResult(live *monitor.Log, sid, msg string) (runtime.WorkerResult, error) {
	live.Line("FALHA: " + msg)
	return runtime.WorkerResult{WorkerSessionID: sid, ObservedModel: "unknown", Status: protocol.StatusFailed, Summary: msg}, errors.New(msg)
}

// tracker evaluates Copilot session events. A turn is finished when an
// assistant.turn_end follows an assistant.message without tool requests.
type tracker struct {
	model     string
	notBefore time.Time

	started       bool // our user.message was seen
	lastTools     int
	lastContent   string
	final         string
	done          bool
	failure       string
	modelSwitched bool
	observedModel string
	usage         protocol.Usage
	hasUsage      bool
	exitCode      *int
}

func newTracker(model string, start time.Time) *tracker {
	t := &tracker{model: model}
	if !start.IsZero() {
		t.notBefore = start.Add(-2 * time.Second)
	}
	return t
}

func (t *tracker) decided() bool { return t.done || t.failure != "" }

func (t *tracker) feed(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || t.decided() {
		return
	}
	var ev struct {
		Type      string          `json:"type"`
		Timestamp time.Time       `json:"timestamp"`
		Ephemeral bool            `json:"ephemeral"`
		Data      json.RawMessage `json:"data"`
		ExitCode  *int            `json:"exitCode"`
	}
	if json.Unmarshal(line, &ev) != nil {
		return
	}
	if !t.notBefore.IsZero() && !ev.Timestamp.IsZero() && ev.Timestamp.Before(t.notBefore) {
		return
	}
	var d struct {
		Model        string            `json:"model"`
		NewModel     string            `json:"newModel"`
		Message      string            `json:"message"`
		ErrorType    string            `json:"errorType"`
		Content      string            `json:"content"`
		ToolRequests []json.RawMessage `json:"toolRequests"`
		Usage        *struct {
			Model           string `json:"model"`
			InputTokens     int    `json:"inputTokens"`
			OutputTokens    int    `json:"outputTokens"`
			CacheReadTokens int    `json:"cacheReadTokens"`
			ReasoningTokens int    `json:"reasoningTokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(ev.Data, &d)

	switch ev.Type {
	case "session.model_change":
		if d.NewModel != "" && !strings.EqualFold(d.NewModel, t.model) {
			t.modelSwitched = true
			t.failure = fmt.Sprintf("o Copilot trocou o modelo pedido (%s) por %q; a ponte não aceita substituição silenciosa", t.model, d.NewModel)
		}
	case "session.error":
		if d.ErrorType == "model" {
			t.modelSwitched = true
			t.failure = "modelo indisponível no Copilot: " + d.Message
		} else {
			t.failure = "erro na sessão do Copilot: " + d.Message
		}
	case "user.message":
		t.started = true
	case "session.usage_record":
		if d.Usage != nil {
			t.hasUsage = true
			t.usage.InputTokens += d.Usage.InputTokens
			t.usage.OutputTokens += d.Usage.OutputTokens
			t.usage.CacheReadTokens += d.Usage.CacheReadTokens
			t.usage.ThinkingTokens += d.Usage.ReasoningTokens
			t.usage.TotalTokens = t.usage.InputTokens + t.usage.OutputTokens
		}
	case "assistant.message":
		if d.Model != "" {
			t.observedModel = d.Model
			if !strings.EqualFold(d.Model, t.model) && t.failure == "" {
				t.modelSwitched = true
				t.failure = fmt.Sprintf("o Copilot respondeu com o modelo %q, diferente do pedido (%s)", d.Model, t.model)
			}
		}
		t.lastTools, t.lastContent = len(d.ToolRequests), strings.TrimSpace(d.Content)
	case "assistant.turn_end":
		if t.started && t.lastTools == 0 && t.lastContent != "" {
			t.final, t.done = t.lastContent, true
		}
	case "result":
		t.exitCode = ev.ExitCode
		if ev.ExitCode != nil && *ev.ExitCode != 0 && !t.done {
			t.failure = fmt.Sprintf("o Copilot terminou com código %d", *ev.ExitCode)
		}
	}
}

func (t *tracker) result(sid string) (runtime.WorkerResult, error) {
	res := runtime.WorkerResult{WorkerSessionID: sid, ObservedModel: t.observedModel}
	if res.ObservedModel == "" {
		res.ObservedModel = "unknown"
	}
	if t.hasUsage {
		u := t.usage
		res.Usage = &u
	}
	switch {
	case t.failure != "":
		res.Status, res.Summary = protocol.StatusFailed, t.failure
	case !t.done:
		res.Status, res.Summary = protocol.StatusFailed, "o Copilot terminou sem resposta final do modelo"
	default:
		res.Status, res.Summary = protocol.StatusSucceeded, t.final
		return res, nil
	}
	return res, errors.New(res.Summary)
}
