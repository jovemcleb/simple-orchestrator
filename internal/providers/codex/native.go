package codex

import (
	"bufio"
	"bytes"
	"context"
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
	"github.com/jovemcleb/simple-orchestrator/internal/runtime"
)

// NativeLauncher starts argv in its own visible console. It returns a channel
// closed when the process exits and a function that kills the process tree.
type NativeLauncher func(dir string, argv []string) (<-chan struct{}, func(), error)

// resolveInteractive returns the program and leading args that run the Codex TUI
// directly. npm's codex.cmd is bypassed because cmd.exe re-parses arguments and
// would mangle a multi-line prompt with quotes or & | < > characters.
func resolveInteractive(cli string) ([]string, error) {
	return ResolveNodeCLI(cli, "@openai", "codex", "bin", "codex.js")
}

// ResolveNodeCLI returns argv[0:] that runs an npm-installed CLI without its
// .cmd/.ps1 shim: node.exe plus the package entry script under node_modules.
// Native .exe paths are returned as is.
func ResolveNodeCLI(cli string, entry ...string) ([]string, error) {
	lower := strings.ToLower(cli)
	switch {
	case strings.HasSuffix(lower, ".exe"):
		return []string{cli}, nil
	case strings.HasSuffix(lower, ".cmd"), strings.HasSuffix(lower, ".ps1"):
		dir := filepath.Dir(cli)
		js := filepath.Join(append([]string{dir, "node_modules"}, entry...)...)
		if _, err := os.Stat(js); err != nil {
			return nil, fmt.Errorf("modo interativo: %s não encontrado ao lado de %s", filepath.Join(entry...), cli)
		}
		node := filepath.Join(dir, "node.exe")
		if _, err := os.Stat(node); err != nil {
			if node, err = exec.LookPath("node"); err != nil {
				return nil, errors.New("modo interativo: node.exe não encontrado")
			}
		}
		return []string{node, js}, nil
	case filepath.Ext(cli) == "":
		p, err := exec.LookPath(cli)
		if err != nil {
			return nil, fmt.Errorf("modo interativo: %q não encontrado no PATH", cli)
		}
		if filepath.Ext(p) == "" {
			return nil, fmt.Errorf("modo interativo: não sei executar %q", p)
		}
		return ResolveNodeCLI(p, entry...)
	}
	return nil, fmt.Errorf("modo interativo: não sei executar %q", cli)
}

// buildInteractiveArgs mirrors BuildArgs for the TUI: no --json, no stdin prompt,
// approvals disabled like `codex exec`, and the prompt as the last argument.
func buildInteractiveArgs(cmd protocol.CommandEnvelope, workspace, prompt string) []string {
	model, effort := protocol.ResolveModelAndEffort(protocol.ProviderCodex, cmd.Profile.RequestedModel, cmd.Profile.Effort)
	var args []string
	resumeID := strings.TrimSpace(cmd.WorkerSessionID)
	if resumeID != "" {
		args = append(args, "resume")
	}
	if model != "" && model != "default" {
		args = append(args, "--model", model)
	}
	if effort != "" {
		args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", effort))
	}
	if cmd.Profile.ReadOnly {
		args = append(args, "--sandbox", "read-only")
	}
	args = append(args, "--ask-for-approval", "never")
	if workspace != "" {
		args = append(args, "--cd", workspace)
	}
	if resumeID != "" {
		args = append(args, resumeID)
	}
	return append(args, prompt)
}

func sessionsRoot(override string) string {
	if override != "" {
		return override
	}
	if h := strings.TrimSpace(os.Getenv("CODEX_HOME")); h != "" {
		return filepath.Join(h, "sessions")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "sessions")
}

// findRollout locates this run's session file. For resumed sessions the id is in
// the file name; for new ones the unique nonce in the prompt identifies it.
func findRollout(root, sessionID, nonce string, since time.Time) string {
	var best string
	var bestMod time.Time
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if sessionID != "" {
			if strings.HasSuffix(p, "-"+sessionID+".jsonl") {
				best = p
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(since.Add(-2*time.Second)) || !info.ModTime().After(bestMod) {
			return nil
		}
		if fileContains(p, nonce, 4<<20) {
			best, bestMod = p, info.ModTime()
		}
		return nil
	})
	return best
}

func fileContains(path, needle string, limit int64) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, _ := f.Read(buf)
	return bytes.Contains(buf[:n], []byte(needle))
}

type rolloutOutcome struct {
	SessionID string
	Model     string
	Completed bool
	Aborted   bool
	ErrorMsg  string
	LastMsg   string
	Usage     *protocol.Usage
}

func (o rolloutOutcome) decided() bool { return o.Completed || o.Aborted || o.ErrorMsg != "" }

// parseRollout reads the events Codex itself recorded. Turn events older than
// notBefore are ignored, so earlier turns of a resumed session never count.
func parseRollout(path string, notBefore time.Time) (rolloutOutcome, error) {
	var out rolloutOutcome
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()
	cutoff := notBefore.Add(-2 * time.Second)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		var ev struct {
			Timestamp time.Time `json:"timestamp"`
			Type      string    `json:"type"`
			Payload   struct {
				Type             string `json:"type"`
				ID               string `json:"id"`
				Model            string `json:"model"`
				Message          string `json:"message"`
				LastAgentMessage string `json:"last_agent_message"`
				Info             *struct {
					Total struct {
						Input     int `json:"input_tokens"`
						Cached    int `json:"cached_input_tokens"`
						Output    int `json:"output_tokens"`
						Reasoning int `json:"reasoning_output_tokens"`
						Total     int `json:"total_tokens"`
					} `json:"total_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		p := ev.Payload
		if ev.Type == "session_meta" {
			out.SessionID = p.ID
			continue
		}
		if ev.Timestamp.Before(cutoff) {
			continue
		}
		switch {
		case ev.Type == "turn_context" && p.Model != "":
			out.Model = p.Model
		case ev.Type != "event_msg" || out.decided():
		case p.Type == "task_complete":
			out.Completed, out.LastMsg = true, p.LastAgentMessage
		case p.Type == "turn_aborted":
			out.Aborted = true
		case p.Type == "error":
			out.ErrorMsg = p.Message
		case p.Type == "token_count" && p.Info != nil:
			t := p.Info.Total
			out.Usage = &protocol.Usage{InputTokens: t.Input, OutputTokens: t.Output, ThinkingTokens: t.Reasoning, CacheReadTokens: t.Cached, TotalTokens: t.Total}
		}
	}
	return out, sc.Err()
}

func failNative(live *monitor.Log, msg string) (runtime.WorkerResult, error) {
	live.Line("FALHA: " + msg)
	return runtime.WorkerResult{Status: protocol.StatusFailed, Summary: msg}, errors.New(msg)
}

// runNative opens the interactive Codex TUI with the task as its first prompt.
// The TUI does not exit by itself, so the outcome of the first turn is read
// from the session file Codex writes (R07); the window stays open afterwards.
func (a *Adapter) runNative(ctx context.Context, cmd protocol.CommandEnvelope, cli, workspace string, isNewChat bool) (runtime.WorkerResult, error) {
	live := monitor.FromContext(ctx)
	start := time.Now()

	argv0, err := resolveInteractive(cli)
	if err != nil {
		return failNative(live, err.Error())
	}
	nonce := fmt.Sprintf("myo-attempt-%s-%s-%s-%d", cmd.RunID, cmd.TaskID, cmd.AttemptID, start.UnixNano())
	prompt := BuildPrompt(cmd.Spec) + "\n[" + nonce + "]"
	argv := append(argv0, buildInteractiveArgs(cmd, workspace, prompt)...)

	launch := a.Launch
	if launch == nil {
		launch = DefaultNativeLauncher
	}
	exited, kill, err := launch(workspace, argv)
	if err != nil {
		return failNative(live, "não foi possível abrir a janela interativa do Codex: "+err.Error())
	}
	live.Line("Janela interativa do Codex aberta; aguardando o primeiro turno terminar.")

	resumeID := strings.TrimSpace(cmd.WorkerSessionID)
	root := sessionsRoot(a.SessionsDir)
	var rollout string
	var oc rolloutOutcome
	check := func() bool {
		if rollout == "" {
			rollout = findRollout(root, resumeID, nonce, start)
			if rollout == "" {
				return false
			}
		}
		if o, err := parseRollout(rollout, start); err == nil {
			oc = o
		}
		return oc.decided()
	}

	poll := a.PollInterval
	if poll <= 0 {
		poll = 700 * time.Millisecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
wait:
	for {
		select {
		case <-ctx.Done():
			kill()
			return failNative(live, "execução cancelada ou expirada antes do turno terminar; janela do Codex encerrada: "+ctx.Err().Error())
		case <-exited:
			if check() {
				break wait
			}
			return failNative(live, "a janela do Codex foi fechada antes do turno terminar; a tarefa foi interrompida")
		case <-ticker.C:
			if check() {
				break wait
			}
		}
	}

	res := runtime.WorkerResult{WorkerSessionID: oc.SessionID, ObservedModel: oc.Model, Summary: strings.TrimSpace(oc.LastMsg)}
	if res.WorkerSessionID == "" {
		res.WorkerSessionID = resumeID
	}
	if res.ObservedModel == "" {
		res.ObservedModel = "unknown"
	}
	if resumeID == "" {
		res.Usage = oc.Usage // cumulative per session, so only reliable for new chats
	}
	if !oc.Completed {
		reason := "turno abortado"
		if oc.ErrorMsg != "" {
			reason = oc.ErrorMsg
		}
		res.Status, res.Summary = protocol.StatusFailed, "o Codex não concluiu o turno: "+reason
		live.Line("FALHA: " + res.Summary)
		return res, errors.New(res.Summary)
	}
	res.Status = protocol.StatusSucceeded
	live.Line("Turno concluído. A janela continua aberta; o que for digitado nela depois não entra neste resultado.")

	if res.WorkerSessionID != "" {
		syncCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = resolveOrSyncCodexDesktop(syncCtx, map[string]any{
			"mode":        "sync",
			"workspace":   workspace,
			"session_id":  res.WorkerSessionID,
			"chat_title":  cmd.EffectiveChatTitle(),
			"is_new":      isNewChat,
			"write_index": true,
		})
	}
	return res, nil
}
