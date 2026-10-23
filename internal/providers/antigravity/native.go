package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jovemcleb/simple-orchestrator/internal/monitor"
	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
	"github.com/jovemcleb/simple-orchestrator/internal/providers/codex"
	"github.com/jovemcleb/simple-orchestrator/internal/runtime"
)

// agyTurnProbePy reports the state of the conversation that carries the nonce.
// conversation_summaries.db gives the run status (RUNNING/IDLE, not_fully_idle);
// the transcript gives the steps recorded after the delegated prompt.
const agyTurnProbePy = `
import sqlite3, os, glob, json, sys
req = json.loads(sys.stdin.read())
nonce, since, cid = req["nonce"], req["since"], req.get("conversation_id", "")
base = req.get("base") or os.path.expanduser("~/.gemini/antigravity-cli")
def transcript(c):
    return os.path.join(base, "brain", c, ".system_generated", "logs", "transcript.jsonl")
if not cid:
    for p in glob.glob(os.path.join(base, "brain", "*", ".system_generated", "logs", "transcript.jsonl")):
        try:
            if os.path.getmtime(p) < since: continue
            with open(p, encoding="utf-8", errors="ignore") as f:
                if nonce in f.read(): cid = p.split(os.sep)[-4]; break
        except OSError:
            pass
out = {"conversation_id": cid, "found_input": False, "steps": [], "status": "", "not_fully_idle": True, "killed": False}
if cid and os.path.exists(transcript(cid)):
    seen = False
    with open(transcript(cid), encoding="utf-8", errors="ignore") as f:
        for line in f:
            try: s = json.loads(line)
            except Exception: continue
            if not seen:
                if s.get("type") == "USER_INPUT" and nonce in (s.get("content") or ""): seen = True
                continue
            out["steps"].append({k: s.get(k) for k in ("type", "status", "content", "input_tokens", "output_tokens", "cache_read_tokens")} | {"tool_calls": len(s.get("tool_calls") or [])})
    out["found_input"] = seen
    db = os.path.join(base, "conversation_summaries.db")
    if os.path.exists(db):
        try:
            c = sqlite3.connect(f"file:{db}?mode=ro", uri=True, timeout=2)
            r = c.execute("SELECT status, not_fully_idle, killed FROM conversation_summaries WHERE conversation_id = ?", (cid,)).fetchone()
            if r: out["status"], out["not_fully_idle"], out["killed"] = r[0], bool(r[1]), bool(r[2])
        except Exception:
            pass
print(json.dumps(out))
`

type agyStep struct {
	Type            string `json:"type"`
	Status          string `json:"status"`
	Content         string `json:"content"`
	InputTokens     int    `json:"input_tokens"`
	OutputTokens    int    `json:"output_tokens"`
	CacheReadTokens int    `json:"cache_read_tokens"`
	ToolCalls       int    `json:"tool_calls"`
}

type agyTurnState struct {
	ConversationID string    `json:"conversation_id"`
	FoundInput     bool      `json:"found_input"`
	Steps          []agyStep `json:"steps"`
	Status         string    `json:"status"`
	NotFullyIdle   bool      `json:"not_fully_idle"`
	Killed         bool      `json:"killed"`
}

// TurnProbe returns the current state of the delegated turn; tests replace it.
type TurnProbe func(ctx context.Context, nonce string, since time.Time, conversationID string) (agyTurnState, error)

func pythonTurnProbe(ctx context.Context, nonce string, since time.Time, conversationID string) (agyTurnState, error) {
	in, _ := json.Marshal(map[string]any{"nonce": nonce, "since": float64(since.Unix()) - 2, "conversation_id": conversationID})
	c := exec.CommandContext(ctx, "python", "-c", agyTurnProbePy)
	c.Stdin = strings.NewReader(string(in))
	c.Env = append(c.Environ(), "PYTHONIOENCODING=utf-8")
	out, err := c.Output()
	if err != nil {
		return agyTurnState{}, err
	}
	var st agyTurnState
	return st, json.Unmarshal(out, &st)
}

// turnOutcome decides whether the delegated turn is over. It requires the
// conversation to be fully idle and the transcript to hold a final answer
// (a model response without tool calls) after the delegated prompt.
func turnOutcome(st agyTurnState) (done bool, final *agyStep, failure string) {
	if st.Killed {
		return true, nil, "a conversa foi encerrada (killed)"
	}
	if !st.FoundInput || st.Status != "CASCADE_RUN_STATUS_IDLE" || st.NotFullyIdle || len(st.Steps) == 0 {
		return false, nil, ""
	}
	last := st.Steps[len(st.Steps)-1]
	if last.Status == "RUNNING" {
		return false, nil, ""
	}
	if last.Status == "ERROR" {
		msg := strings.TrimSpace(last.Content)
		if msg == "" {
			msg = "o último passo terminou com erro"
		}
		return true, nil, msg
	}
	for i := len(st.Steps) - 1; i >= 0; i-- {
		s := st.Steps[i]
		if s.Type == "PLANNER_RESPONSE" && s.ToolCalls == 0 && s.Status == "DONE" && strings.TrimSpace(s.Content) != "" {
			return true, &st.Steps[i], ""
		}
	}
	return true, nil, "o turno terminou sem resposta final do modelo"
}

func failNative(live *monitor.Log, sid, msg string) (runtime.WorkerResult, error) {
	live.Line("FALHA: " + msg)
	return runtime.WorkerResult{WorkerSessionID: sid, ObservedModel: "unknown", Status: protocol.StatusFailed, Summary: msg}, errors.New(msg)
}

// buildInteractiveArgs mirrors BuildArgs for `agy -i`: same mode, permissions,
// conversation, project, model and effort, without the stream-json print flags.
func (a *Adapter) buildInteractiveArgs(cmd protocol.CommandEnvelope, prompt string) []string {
	model, effort := protocol.ResolveModelAndEffort(protocol.ProviderAntigravity, cmd.Profile.RequestedModel, cmd.Profile.Effort)
	args := []string{"--mode", "auto", "--dangerously-skip-permissions"}
	if sid := strings.TrimSpace(cmd.WorkerSessionID); sid != "" {
		args = append(args, "--conversation", sid)
	}
	if p := strings.TrimSpace(cmd.WorkerProject); p != "" {
		args = append(args, "--project", p)
	}
	if model != "" && model != "default" {
		args = append(args, "--model", model)
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}
	return append(args, "-i", prompt)
}

// runNative opens the interactive Antigravity CLI with the task as its first
// prompt and returns when that turn ends; the window stays open afterwards.
// Global MCPs are not isolated here: the hang seen in T01 was specific to the
// headless print mode, and the interactive CLI is the user's normal setup.
func (a *Adapter) runNative(ctx context.Context, cmd protocol.CommandEnvelope, cli string, isNewChat bool, syncIDE func(sid string, notifyUI bool)) (runtime.WorkerResult, error) {
	live := monitor.FromContext(ctx)
	start := time.Now()
	nonce := fmt.Sprintf("myo-attempt-%s-%s-%s-%d", cmd.RunID, cmd.TaskID, cmd.AttemptID, start.UnixNano())
	prompt := codex.BuildPrompt(cmd.Spec) + "\n[" + nonce + "]"

	launch := a.Launch
	if launch == nil {
		launch = codex.DefaultNativeLauncher
	}
	exited, kill, err := launch(cmd.Workspace, append([]string{cli}, a.buildInteractiveArgs(cmd, prompt)...))
	if err != nil {
		return failNative(live, cmd.WorkerSessionID, "não foi possível abrir a janela interativa do Antigravity: "+err.Error())
	}
	live.Line("Janela interativa do Antigravity aberta; aguardando o primeiro turno terminar.")

	probe := a.Probe
	if probe == nil {
		probe = pythonTurnProbe
	}
	poll := a.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	sid := strings.TrimSpace(cmd.WorkerSessionID)
	synced := false
	var final *agyStep
	var st agyTurnState
	check := func() (bool, string) {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		s, err := probe(pctx, nonce, start, sid)
		if err != nil {
			return false, ""
		}
		st = s
		if sid == "" && s.ConversationID != "" {
			sid = s.ConversationID
		}
		if sid != "" && !synced {
			synced = true
			live.Line("Sessão iniciada: " + sid)
			syncIDE(sid, true)
		}
		done, f, failure := turnOutcome(s)
		final = f
		return done, failure
	}

	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	var failure string
wait:
	for {
		select {
		case <-ctx.Done():
			kill()
			return failNative(live, sid, "execução cancelada ou expirada antes do turno terminar; janela do Antigravity encerrada: "+ctx.Err().Error())
		case <-exited:
			if done, f := check(); done {
				failure = f
				break wait
			}
			return failNative(live, sid, "a janela do Antigravity foi fechada antes do turno terminar; a tarefa foi interrompida")
		case <-ticker.C:
			if done, f := check(); done {
				failure = f
				break wait
			}
		}
	}

	res := runtime.WorkerResult{WorkerSessionID: sid, ObservedModel: "unknown"}
	if isNewChat {
		u := &protocol.Usage{}
		for _, s := range st.Steps {
			u.InputTokens += s.InputTokens
			u.OutputTokens += s.OutputTokens
			u.CacheReadTokens += s.CacheReadTokens
		}
		u.TotalTokens = u.InputTokens + u.OutputTokens
		res.Usage = u
	}
	if failure != "" || final == nil {
		if failure == "" {
			failure = "o turno terminou sem resposta final do modelo"
		}
		res.Status, res.Summary = protocol.StatusFailed, "o Antigravity não concluiu o turno: "+failure
		live.Line("FALHA: " + res.Summary)
		return res, errors.New(res.Summary)
	}
	res.Status, res.Summary = protocol.StatusSucceeded, strings.TrimSpace(final.Content)
	live.Line("Turno concluído. A janela continua aberta; o que for digitado nela depois não entra neste resultado.")
	syncIDE(sid, true)
	return res, nil
}
