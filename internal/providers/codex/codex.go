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
	"strings"
	"sync"
	"time"

	"github.com/jovemcleb/simple-orchestrator/internal/monitor"
	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
	"github.com/jovemcleb/simple-orchestrator/internal/runtime"
)

type Adapter struct {
	CLIPath string
	// NativeWatch opens the interactive Codex TUI in its own window (--watch-native)
	// instead of the structured --json stream.
	NativeWatch  bool
	Launch       NativeLauncher
	SessionsDir  string
	PollInterval time.Duration
}

func NewAdapter(cliPath string) *Adapter {
	if strings.TrimSpace(cliPath) == "" {
		if p, err := exec.LookPath("codex"); err == nil {
			cliPath = p
		} else {
			cliPath = "codex"
		}
	}
	return &Adapter{CLIPath: cliPath}
}

func BuildPrompt(spec protocol.TaskSpec) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Target: %s\n", spec.Target))
	if spec.Change != "" {
		b.WriteString(fmt.Sprintf("Change: %s\n", spec.Change))
	}
	if spec.Constraints != "" {
		b.WriteString(fmt.Sprintf("Constraints: %s\n", spec.Constraints))
	}
	if spec.Ownership != "" {
		b.WriteString(fmt.Sprintf("Ownership: %s\n", spec.Ownership))
	}
	b.WriteString(fmt.Sprintf("Observable acceptance: %s\n", spec.Acceptance))
	if spec.Prompt != "" {
		b.WriteString("\n" + spec.Prompt + "\n")
	}
	return b.String()
}

func (a *Adapter) BuildArgs(cmd protocol.CommandEnvelope) []string {
	model, effort := protocol.ResolveModelAndEffort(protocol.ProviderCodex, cmd.Profile.RequestedModel, cmd.Profile.Effort)
	var args []string
	if strings.TrimSpace(cmd.WorkerSessionID) != "" {
		args = []string{"exec", "resume", "--json", "--skip-git-repo-check"}
		if model != "" && model != "default" {
			args = append(args, "--model", model)
		}
		if effort != "" {
			args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", effort))
		}
		// Pass "-" to read multi-line prompt from stdin, avoiding Windows .cmd newline truncation.
		args = append(args, strings.TrimSpace(cmd.WorkerSessionID), "-")
		return args
	}

	args = []string{"exec", "--json", "--skip-git-repo-check"}
	if cmd.Profile.ReadOnly {
		args = append(args, "--sandbox", "read-only")
	}
	if model != "" && model != "default" {
		args = append(args, "--model", model)
	}
	if effort != "" {
		args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", effort))
	}
	args = append(args, "-")
	return args
}

const codexResolveAndSyncPy = `
import json, os, re, sqlite3, sys, unicodedata
from datetime import datetime, timezone

def norm(s):
    s = unicodedata.normalize('NFKD', s or '').encode('ascii', 'ignore').decode('ascii').lower()
    s = re.sub(r'[^a-z0-9\s]+', '', s)
    return [w for w in s.split() if w]

def match_text(query, candidate):
    qt = norm(query)
    ct = norm(candidate)
    if not qt or not ct:
        return False
    qj = " ".join(qt)
    cj = " ".join(ct)
    if qj in cj or cj in qj:
        return True
    matched = 0
    for q in qt:
        p = q[:5]
        if any(c.startswith(p) or q.startswith(c[:5]) for c in ct if len(c) >= 2):
            matched += 1
    return matched == len(qt)

def same_dir(a, b):
    try:
        ca = (a or '').replace('\\\\?\\', '')
        cb = (b or '').replace('\\\\?\\', '')
        return os.path.exists(ca) and os.path.exists(cb) and os.path.samefile(ca, cb)
    except Exception:
        return False

req = json.loads(sys.stdin.read())
mode = req.get("mode")
db_path = os.path.expanduser("~/.codex/state_5.sqlite")
idx_path = os.path.expanduser("~/.codex/session_index.jsonl")

if not os.path.exists(db_path):
    print(json.dumps({"ok": True, "workspace": req.get("workspace", ""), "session_id": ""}))
    sys.exit(0)

conn = sqlite3.connect(db_path, timeout=5.0)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

workspace = req.get("workspace", "")
project_q = (req.get("project", "") or "").strip()
chat_q = (req.get("chat", "") or "").strip()

project_roots = []
try:
    for r in cur.execute("SELECT p.name, pr.path FROM projects p JOIN project_roots pr ON p.id = pr.project_id"):
        if workspace and same_dir(workspace, r["path"]):
            workspace = r["path"]
        if project_q and (match_text(project_q, r["name"]) or match_text(project_q, os.path.basename(r["path"]))):
            project_roots.append(r["path"])
            if not workspace or same_dir(workspace, r["path"]):
                workspace = r["path"]
except Exception:
    pass

if mode == "resolve":
    if not chat_q:
        print(json.dumps({"ok": True, "workspace": workspace, "session_id": ""}))
        sys.exit(0)
    idx_names = {}
    if os.path.exists(idx_path):
        with open(idx_path, "r", encoding="utf-8", errors="ignore") as f:
            for line in f:
                try:
                    obj = json.loads(line)
                    if obj.get("id") and obj.get("thread_name"):
                        idx_names[obj["id"]] = obj["thread_name"]
                except Exception:
                    pass
    rows = cur.execute("SELECT id, cwd, title, name FROM threads WHERE archived = 0 ORDER BY updated_at DESC").fetchall()
    for r in rows:
        tid = r["id"]
        cand_name = r["name"] or idx_names.get(tid, "") or ""
        cand_title = r["title"] or ""
        if match_text(chat_q, cand_name) or match_text(chat_q, cand_title):
            if project_roots:
                if any(same_dir(rt, r["cwd"]) for rt in project_roots):
                    print(json.dumps({"ok": True, "workspace": workspace, "session_id": tid}))
                    sys.exit(0)
            elif workspace:
                if same_dir(workspace, r["cwd"]):
                    print(json.dumps({"ok": True, "workspace": workspace, "session_id": tid}))
                    sys.exit(0)
            else:
                print(json.dumps({"ok": True, "workspace": workspace, "session_id": tid}))
                sys.exit(0)
    print(json.dumps({"ok": False, "error": f"codex chat {chat_q!r} not found in project {project_q or workspace!r}"}))
    sys.exit(0)

def desktop_cwd(ws):
    # Codex Desktop stores cwd as an extended-length real path (\\?\C:\...); junction aliases hide threads.
    if not ws:
        return ""
    try:
        for r in cur.execute("SELECT cwd FROM threads WHERE originator = 'Codex Desktop' AND source = 'vscode' AND cwd LIKE '\\\\?\\%' ORDER BY updated_at DESC LIMIT 200"):
            if same_dir(ws, r["cwd"]):
                return r["cwd"]
    except Exception:
        pass
    real = os.path.realpath(ws.replace('\\\\?\\', ''))
    return real if real.startswith('\\\\?\\') else '\\\\?\\' + real

if mode == "sync":
    tid = (req.get("session_id") or "").strip()
    title = (req.get("chat_title") or "").strip()
    is_new = bool(req.get("is_new", False))
    write_index = bool(req.get("write_index", True))
    row = cur.execute("SELECT rollout_path, name, title, cwd FROM threads WHERE id = ?", (tid,)).fetchone()
    if row:
        rollout_path = (row["rollout_path"] or "").replace("\\\\?\\", "")
        if rollout_path and os.path.exists(rollout_path):
            try:
                with open(rollout_path, "r", encoding="utf-8") as f:
                    lines = f.readlines()
                if lines:
                    first = json.loads(lines[0])
                    payload = first.get("payload", {})
                    changed = False
                    if payload.get("source") != "vscode" or payload.get("originator") != "Codex Desktop":
                        payload["source"] = "vscode"
                        payload["originator"] = "Codex Desktop"
                        changed = True
                    if workspace and payload.get("cwd") != workspace:
                        payload["cwd"] = workspace
                        payload["runtime_workspace_roots"] = [workspace]
                        changed = True
                    if changed:
                        first["payload"] = payload
                        lines[0] = json.dumps(first, ensure_ascii=False) + "\n"
                        with open(rollout_path, "w", encoding="utf-8") as f:
                            f.writelines(lines)
            except Exception:
                pass
        existing_name = (row["name"] or "").strip()
        final_name = existing_name if (existing_name and not is_new) else (title or existing_name or "Delegated Task")
        final_cwd = desktop_cwd(workspace) if workspace else (row["cwd"] or "")
        cur.execute("""
            UPDATE threads
            SET source = 'vscode',
                originator = 'Codex Desktop',
                cwd = CASE WHEN ? != '' THEN ? ELSE cwd END,
                rollout_path = ?,
                name = ?,
                title = CASE WHEN ? THEN ? ELSE title END
            WHERE id = ?
        """, (final_cwd, final_cwd, rollout_path or row["rollout_path"], final_name, 1 if is_new else 0, final_name, tid))
        conn.commit()
        if write_index and (is_new or not existing_name):
            now_iso = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
            with open(idx_path, "a", encoding="utf-8") as f:
                f.write(json.dumps({"id": tid, "thread_name": final_name, "updated_at": now_iso}, ensure_ascii=False) + "\n")
    conn.close()
    print(json.dumps({"ok": True}))
`

func resolveOrSyncCodexDesktop(ctx context.Context, payload map[string]any) (map[string]any, error) {
	inBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	proc := exec.CommandContext(ctx, "python", "-c", codexResolveAndSyncPy)
	proc.Stdin = bytes.NewReader(inBytes)
	var out, stderr bytes.Buffer
	proc.Stdout = &out
	proc.Stderr = &stderr
	if err := proc.Run(); err != nil {
		return nil, fmt.Errorf("codex desktop sqlite helper failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	var resp map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		return nil, err
	}
	if ok, _ := resp["ok"].(bool); !ok {
		errMsg, _ := resp["error"].(string)
		if errMsg == "" {
			errMsg = "codex desktop lookup failed"
		}
		return nil, errors.New(errMsg)
	}
	return resp, nil
}

func (a *Adapter) RunTask(ctx context.Context, cmd protocol.CommandEnvelope) (runtime.WorkerResult, error) {
	runCtx := ctx
	var cancel context.CancelFunc = func() {}
	if cmd.Profile.TimeoutSeconds > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(cmd.Profile.TimeoutSeconds)*time.Second)
	}
	defer cancel()

	isNewChat := strings.TrimSpace(cmd.WorkerSessionID) == "" && strings.TrimSpace(cmd.WorkerChat) == ""
	canonicalWorkspace := strings.TrimSpace(cmd.Workspace)

	if resolved, err := resolveOrSyncCodexDesktop(context.Background(), map[string]any{
		"mode":      "resolve",
		"workspace": canonicalWorkspace,
		"project":   cmd.WorkerProject,
		"chat":      cmd.WorkerChat,
	}); err != nil {
		if strings.TrimSpace(cmd.WorkerChat) != "" {
			return runtime.WorkerResult{Status: protocol.StatusFailed, Summary: err.Error()}, err
		}
	} else {
		if ws, _ := resolved["workspace"].(string); strings.TrimSpace(ws) != "" {
			canonicalWorkspace = strings.TrimSpace(ws)
		}
		if sid, _ := resolved["session_id"].(string); strings.TrimSpace(sid) != "" && strings.TrimSpace(cmd.WorkerSessionID) == "" {
			cmd.WorkerSessionID = strings.TrimSpace(sid)
			isNewChat = false
		}
	}

	cli := cmd.Profile.CLIPath
	if strings.TrimSpace(cli) == "" {
		cli = a.CLIPath
	}

	if a.NativeWatch {
		return a.runNative(runCtx, cmd, cli, canonicalWorkspace, isNewChat)
	}

	args := a.BuildArgs(cmd)
	proc := exec.CommandContext(runCtx, cli, args...)
	if canonicalWorkspace != "" {
		proc.Dir = canonicalWorkspace
	}
	proc.Env = append(os.Environ(), "CI=true")

	prompt := BuildPrompt(cmd.Spec)
	proc.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	proc.Stderr = &stderr

	stdoutPipe, err := proc.StdoutPipe()
	if err != nil {
		return runtime.WorkerResult{Status: protocol.StatusFailed, Summary: err.Error()}, err
	}
	if err := proc.Start(); err != nil {
		return runtime.WorkerResult{Status: protocol.StatusFailed, Summary: err.Error()}, err
	}

	var sidMu sync.Mutex
	liveSessionID := strings.TrimSpace(cmd.WorkerSessionID)
	syncDesktop := func(sid string, writeIndex bool) {
		if sid == "" {
			return
		}
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer syncCancel()
		_, _ = resolveOrSyncCodexDesktop(syncCtx, map[string]any{
			"mode":        "sync",
			"workspace":   canonicalWorkspace,
			"session_id":  sid,
			"chat_title":  cmd.EffectiveChatTitle(),
			"is_new":      isNewChat,
			"write_index": writeIndex,
		})
	}

	live := monitor.FromContext(ctx)
	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var syncedInitial bool
	for scanner.Scan() {
		line := scanner.Bytes()
		stdout.Write(line)
		stdout.WriteByte('\n')
		live.Raw(line)

		var ev rawCodexEvent
		if err := json.Unmarshal(bytes.TrimSpace(line), &ev); err == nil && ev.ThreadID != "" {
			sidMu.Lock()
			liveSessionID = ev.ThreadID
			sidMu.Unlock()
			if !syncedInitial {
				syncedInitial = true
				syncDesktop(ev.ThreadID, true)
			}
		}
	}

	runErr := proc.Wait()
	res, parseErr := ParseStreamJSONL(stdout.Bytes(), stderr.Bytes(), runErr, liveSessionID)
	if res.WorkerSessionID != "" {
		syncDesktop(res.WorkerSessionID, !syncedInitial)
	}
	return res, parseErr
}

type rawCodexEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id,omitempty"`
	Model    string `json:"model,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Item *struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content,omitempty"`
	} `json:"item,omitempty"`
	Payload *struct {
		Type             string `json:"type"`
		LastAgentMessage string `json:"last_agent_message,omitempty"`
	} `json:"payload,omitempty"`
	Usage *struct {
		InputTokens       int `json:"input_tokens"`
		CachedInputTokens int `json:"cached_input_tokens"`
		OutputTokens      int `json:"output_tokens"`
		OutputDetails     *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"output_tokens_details,omitempty"`
	} `json:"usage,omitempty"`
}

func ParseStreamJSONL(stdout, stderr []byte, exitErr error, fallbackSessionID string) (runtime.WorkerResult, error) {
	res := runtime.WorkerResult{
		WorkerSessionID: fallbackSessionID,
		ObservedModel:   "unknown",
	}

	var messages []string
	var turnCompleted bool
	var turnFailedMsg string

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev rawCodexEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.ThreadID != "" {
			res.WorkerSessionID = ev.ThreadID
		}
		if ev.Model != "" {
			res.ObservedModel = ev.Model
		}
		switch ev.Type {
		case "item.completed":
			if ev.Item != nil && (ev.Item.Type == "agent_message" || ev.Item.Type == "message") {
				if strings.TrimSpace(ev.Item.Text) != "" {
					messages = append(messages, strings.TrimSpace(ev.Item.Text))
				}
				for _, c := range ev.Item.Content {
					if strings.TrimSpace(c.Text) != "" {
						messages = append(messages, strings.TrimSpace(c.Text))
					}
				}
			}
		case "event_msg":
			if ev.Payload != nil && ev.Payload.Type == "task_complete" && strings.TrimSpace(ev.Payload.LastAgentMessage) != "" {
				turnCompleted = true
				messages = append(messages, strings.TrimSpace(ev.Payload.LastAgentMessage))
			}
		case "turn.completed":
			turnCompleted = true
			if ev.Usage != nil {
				thinking := 0
				if ev.Usage.OutputDetails != nil {
					thinking = ev.Usage.OutputDetails.ReasoningTokens
				}
				res.Usage = &protocol.Usage{
					InputTokens:     ev.Usage.InputTokens,
					OutputTokens:    ev.Usage.OutputTokens,
					ThinkingTokens:  thinking,
					CacheReadTokens: ev.Usage.CachedInputTokens,
					TotalTokens:     ev.Usage.InputTokens + ev.Usage.OutputTokens,
				}
			}
		case "turn.failed", "error":
			turnCompleted = false
			if ev.Error != nil && ev.Error.Message != "" {
				turnFailedMsg = ev.Error.Message
			} else if ev.Message != "" {
				turnFailedMsg = ev.Message
			} else {
				turnFailedMsg = "codex reported turn.failed"
			}
		}
	}

	res.Summary = strings.Join(messages, "\n")

	if turnFailedMsg != "" {
		res.Status = protocol.StatusFailed
		res.Summary = turnFailedMsg
		return res, errors.New(turnFailedMsg)
	}
	if exitErr != nil {
		res.Status = protocol.StatusFailed
		errText := strings.TrimSpace(string(stderr))
		if errText == "" {
			errText = exitErr.Error()
		}
		if strings.TrimSpace(res.Summary) != "" {
			res.Summary = fmt.Sprintf("%s (process exit error: %s)", res.Summary, errText)
		} else {
			res.Summary = errText
		}
		return res, fmt.Errorf("codex process failed: %w", exitErr)
	}
	if !turnCompleted || strings.TrimSpace(res.Summary) == "" {
		res.Status = protocol.StatusFailed
		msg := "incomplete codex stream: missing turn.completed or empty agent message"
		res.Summary = msg
		return res, errors.New(msg)
	}

	res.Status = protocol.StatusSucceeded
	return res, nil
}
