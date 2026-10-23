package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jovemcleb/simple-orchestrator/internal/monitor"
	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
	"github.com/jovemcleb/simple-orchestrator/internal/providers/codex"
	"github.com/jovemcleb/simple-orchestrator/internal/runtime"
)

type Adapter struct {
	mu                  sync.Mutex
	CLIPath             string
	MCPConfigPath       string
	AutoSkipHeadlessMCP bool
	AfterIsolateHook    func(configPath string)
	// NativeWatch opens the interactive `agy -i` screen in its own window
	// (--watch-native) instead of the headless stream-json print mode.
	NativeWatch  bool
	Launch       codex.NativeLauncher
	Probe        TurnProbe
	SyncIDE      func(sid string, notifyUI bool)
	PollInterval time.Duration
}

func NewAdapter(cliPath, mcpConfigPath string, autoSkipMCP bool) *Adapter {
	if strings.TrimSpace(cliPath) == "" {
		home, _ := os.UserHomeDir()
		cliPath = filepath.Join(home, "AppData", "Local", "agy", "bin", "agy.EXE")
	}
	if strings.TrimSpace(mcpConfigPath) == "" {
		home, _ := os.UserHomeDir()
		mcpConfigPath = filepath.Join(home, ".gemini", "config", "mcp_config.json")
	}
	return &Adapter{
		CLIPath:             cliPath,
		MCPConfigPath:       mcpConfigPath,
		AutoSkipHeadlessMCP: autoSkipMCP,
	}
}

func (a *Adapter) BuildArgs(cmd protocol.CommandEnvelope) []string {
	timeoutSec := cmd.Profile.TimeoutSeconds
	if timeoutSec < 0 {
		timeoutSec = 0
	}
	model, effort := protocol.ResolveModelAndEffort(protocol.ProviderAntigravity, cmd.Profile.RequestedModel, cmd.Profile.Effort)
	args := []string{
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--mode", "auto",
		"--dangerously-skip-permissions",
		"--disable-slash-commands",
		"--print-timeout", fmt.Sprintf("%ds", timeoutSec),
	}
	if strings.TrimSpace(cmd.WorkerSessionID) != "" {
		args = append(args, "--conversation", strings.TrimSpace(cmd.WorkerSessionID))
	}
	if strings.TrimSpace(cmd.WorkerProject) != "" {
		args = append(args, "--project", strings.TrimSpace(cmd.WorkerProject))
	}
	if model != "" && model != "default" {
		args = append(args, "--model", model)
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}
	return args
}

const agyResolveAndSyncPy = `
import json, os, re, shutil, sqlite3, sys, unicodedata
from urllib.parse import quote, unquote

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

def uri_to_path(uris_json):
    try:
        arr = json.loads(uris_json or "[]")
        if not arr:
            return ""
        u = unquote(arr[0])
        if u.startswith("file:///"):
            u = u[len("file:///"):]
        return os.path.normpath(u)
    except Exception:
        return ""

def path_to_ide_uri(p):
    if not p:
        return ""
    norm_p = os.path.abspath(p).replace("\\", "/")
    if len(norm_p) >= 2 and norm_p[1] == ":":
        drive = norm_p[0].lower()
        rest = norm_p[2:]
        encoded_rest = "/".join(quote(seg, safe="") for seg in rest.split("/"))
        return json.dumps([f"file:///{drive}%3A{encoded_rest}"])
    return json.dumps([f"file:///{quote(norm_p.lstrip('/'), safe='/')}"])

def same_dir(a, b):
    try:
        return bool(a and b and os.path.exists(a) and os.path.exists(b) and os.path.samefile(a, b))
    except Exception:
        return False

def ide_language_server():
    # Locates the running Antigravity IDE language server (CSRF token from its command line, port from netstat).
    import subprocess
    try:
        ps = subprocess.run(["powershell", "-NoProfile", "-Command",
            "Get-CimInstance Win32_Process -Filter \"Name='language_server.exe'\" | Select-Object ProcessId,CommandLine | ConvertTo-Json -Compress"],
            capture_output=True, timeout=15)
        procs = json.loads(ps.stdout.decode("utf-8", errors="ignore") or "[]")
        if isinstance(procs, dict):
            procs = [procs]
        ns = subprocess.run(["netstat", "-ano", "-p", "TCP"], capture_output=True, timeout=15).stdout.decode("latin1", errors="ignore")
    except Exception:
        return []
    found = []
    for p in procs:
        cl = p.get("CommandLine") or ""
        m = re.search(r"--csrf_token\s+(\S+)", cl)
        if not m or not re.search(r"--app_data_dir\s+antigravity(\s|$)", cl):
            continue
        pid = str(p.get("ProcessId"))
        for line in ns.splitlines():
            parts = line.split()
            if len(parts) >= 5 and parts[-1] == pid and parts[-2] == "LISTENING" and parts[1].startswith("127.0.0.1:"):
                found.append((parts[1].split(":")[1], m.group(1)))
    return found

def ide_rpc(port, token, method, payload):
    import urllib.request
    req = urllib.request.Request(
        f"http://127.0.0.1:{port}/exa.language_server_pb.LanguageServerService/{method}",
        data=json.dumps(payload).encode("utf-8"), method="POST",
        headers={"Content-Type": "application/json", "Connect-Protocol-Version": "1", "x-codeium-csrf-token": token})
    with urllib.request.urlopen(req, timeout=5) as resp:
        return json.loads(resp.read().decode("utf-8") or "{}")

def notify_ide(sid, title):
    # The IDE keeps summaries in memory; LoadTrajectory makes it pick up a conversation written by the CLI.
    for port, token in ide_language_server():
        try:
            ide_rpc(port, token, "LoadTrajectory", {"cascadeId": sid})
            summary = (ide_rpc(port, token, "GetAllCascadeTrajectories", {}).get("trajectorySummaries") or {}).get(sid)
            if summary is None:
                continue
            ann = dict(summary.get("annotations") or {})
            if title and not (ann.get("title") or "").strip():
                ann["title"] = title
                ide_rpc(port, token, "UpdateConversationAnnotations", {"cascadeId": sid, "annotations": ann})
            return True
        except Exception:
            continue
    return False

req = json.loads(sys.stdin.read())
mode = req.get("mode")
ide_dir = os.path.expanduser("~/.gemini/antigravity")
cli_dir = os.path.expanduser("~/.gemini/antigravity-cli")
ide_db = os.path.join(ide_dir, "conversation_summaries.db")
cli_db = os.path.join(cli_dir, "conversation_summaries.db")

workspace = (req.get("workspace") or "").strip()
project_q = (req.get("project") or "").strip()
chat_q = (req.get("chat") or "").strip()

if not os.path.exists(ide_db):
    print(json.dumps({"ok": True, "project_id": "", "workspace_uris": "", "session_id": ""}))
    sys.exit(0)

conn = sqlite3.connect(ide_db, timeout=5.0)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

resolved_project_id = ""
resolved_uris = ""
rows = cur.execute("SELECT conversation_id, title, workspace_uris, project_id, last_modified_time FROM conversation_summaries ORDER BY last_modified_time DESC").fetchall()

for r in rows:
    p = uri_to_path(r["workspace_uris"])
    pid = (r["project_id"] or "").strip()
    if pid and pid not in ("default-cli-project", "outside-of-project"):
        if (workspace and same_dir(workspace, p)) or (project_q and match_text(project_q, os.path.basename(p))):
            resolved_project_id = pid
            resolved_uris = r["workspace_uris"]
            break

if workspace:
    resolved_uris = path_to_ide_uri(workspace)

if mode == "resolve":
    sid = (req.get("session_id") or "").strip()
    if not sid and chat_q:
        for r in rows:
            p = uri_to_path(r["workspace_uris"])
            if match_text(chat_q, r["title"] or ""):
                if project_q and not (match_text(project_q, os.path.basename(p)) or match_text(project_q, r["project_id"] or "")):
                    continue
                if workspace and not project_q and p and not same_dir(workspace, p):
                    continue
                sid = r["conversation_id"]
                if not resolved_project_id and r["project_id"]:
                    resolved_project_id = r["project_id"]
                    resolved_uris = r["workspace_uris"]
                break
        if not sid:
            conn.close()
            print(json.dumps({"ok": False, "error": f"antigravity chat {chat_q!r} not found in project {project_q or workspace!r}"}))
            sys.exit(0)
    if sid:
        for ext in ("", "-wal", "-shm"):
            ide_conv = os.path.join(ide_dir, "conversations", f"{sid}.db{ext}")
            cli_conv = os.path.join(cli_dir, "conversations", f"{sid}.db{ext}")
            if os.path.exists(ide_conv):
                os.makedirs(os.path.dirname(cli_conv), exist_ok=True)
                if not os.path.exists(cli_conv) or os.path.getmtime(ide_conv) > os.path.getmtime(cli_conv):
                    shutil.copy2(ide_conv, cli_conv)
        ide_brain = os.path.join(ide_dir, "brain", sid)
        cli_brain = os.path.join(cli_dir, "brain", sid)
        if os.path.exists(ide_brain):
            shutil.copytree(ide_brain, cli_brain, dirs_exist_ok=True)
    conn.close()
    print(json.dumps({"ok": True, "project_id": resolved_project_id, "workspace_uris": resolved_uris, "session_id": sid}))
    sys.exit(0)

if mode == "sync":
    sid = (req.get("session_id") or "").strip()
    title = (req.get("chat_title") or "").strip()
    is_new = bool(req.get("is_new", False))
    ui_title = ""
    if sid:
        for ext in ("", "-wal", "-shm"):
            cli_conv = os.path.join(cli_dir, "conversations", f"{sid}.db{ext}")
            ide_conv = os.path.join(ide_dir, "conversations", f"{sid}.db{ext}")
            if os.path.exists(cli_conv):
                os.makedirs(os.path.dirname(ide_conv), exist_ok=True)
                shutil.copy2(cli_conv, ide_conv)
        cli_brain = os.path.join(cli_dir, "brain", sid)
        ide_brain = os.path.join(ide_dir, "brain", sid)
        if os.path.exists(cli_brain):
            shutil.copytree(cli_brain, ide_brain, dirs_exist_ok=True)
        if os.path.exists(cli_db):
            c_conn = sqlite3.connect(cli_db, timeout=5.0)
            c_conn.row_factory = sqlite3.Row
            c_row = c_conn.execute("SELECT * FROM conversation_summaries WHERE conversation_id = ?", (sid,)).fetchone()
            if c_row:
                d = dict(c_row)
                existing_ide = cur.execute("SELECT title FROM conversation_summaries WHERE conversation_id = ?", (sid,)).fetchone()
                existing_title = (existing_ide["title"] if existing_ide else "") or ""
                if is_new and not existing_title.strip():
                    d["title"] = title or d.get("title") or "Delegated Task"
                elif existing_title.strip():
                    d["title"] = existing_title
                elif not (d.get("title") or "").strip():
                    d["title"] = title or "Delegated Task"
                if resolved_project_id:
                    d["project_id"] = resolved_project_id
                if resolved_uris:
                    d["workspace_uris"] = resolved_uris
                d["source"] = ""
                d["app_data_dir"] = "antigravity"
                cols = list(d.keys())
                placeholders = ",".join(["?"] * len(cols))
                col_names = ",".join([f'"{c}"' for c in cols])
                cur.execute(f"INSERT OR REPLACE INTO conversation_summaries ({col_names}) VALUES ({placeholders})", [d[c] for c in cols])
                conn.commit()
                ui_title = d["title"]
            c_conn.close()
    conn.close()
    notified = notify_ide(sid, ui_title) if (sid and req.get("notify_ui")) else False
    print(json.dumps({"ok": True, "ui_notified": notified}))
`

func resolveOrSyncAntigravityIDE(ctx context.Context, payload map[string]any) (map[string]any, error) {
	inBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	proc := exec.CommandContext(ctx, "python", "-c", agyResolveAndSyncPy)
	proc.Stdin = bytes.NewReader(inBytes)
	var out, stderr bytes.Buffer
	proc.Stdout = &out
	proc.Stderr = &stderr
	if err := proc.Run(); err != nil {
		return nil, fmt.Errorf("antigravity ide sqlite helper failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	var resp map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		return nil, err
	}
	if ok, _ := resp["ok"].(bool); !ok {
		errMsg, _ := resp["error"].(string)
		if errMsg == "" {
			errMsg = "antigravity ide lookup failed"
		}
		return nil, errors.New(errMsg)
	}
	return resp, nil
}

func acquireInterProcessLock(ctx context.Context, lockPath string) (func(), error) {
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "pid=%d time=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
			_ = f.Close()
			return func() {
				_ = os.Remove(lockPath)
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("failed to create MCP lock %q: %w", lockPath, err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for inter-process MCP lock %q: %w", lockPath, ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func IsolateGlobalMCPConfig(ctx context.Context, configPath string) (shaBefore string, restore func() (string, error), err error) {
	if strings.TrimSpace(configPath) == "" {
		return "", func() (string, error) { return "", nil }, nil
	}
	lockPath := configPath + ".myo.lock"
	unlock, err := acquireInterProcessLock(ctx, lockPath)
	if err != nil {
		return "", nil, err
	}

	origBytes, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		unlock()
		return "", func() (string, error) { return "", nil }, nil
	}
	if err != nil {
		unlock()
		return "", nil, err
	}
	sumBefore := sha256.Sum256(origBytes)
	shaBefore = hex.EncodeToString(sumBefore[:])

	var parsed map[string]any
	if err := json.Unmarshal(origBytes, &parsed); err != nil {
		unlock()
		return "", nil, err
	}
	parsed["mcpServers"] = map[string]any{}
	isolatedBytes, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		unlock()
		return "", nil, err
	}
	if err := os.WriteFile(configPath, append(isolatedBytes, '\n'), 0o644); err != nil {
		unlock()
		return "", nil, err
	}

	restoreFn := func() (string, error) {
		defer unlock()
		if writeErr := os.WriteFile(configPath, origBytes, 0o644); writeErr != nil {
			return "", fmt.Errorf("failed to restore mcp_config.json: %w", writeErr)
		}
		afterBytes, readErr := os.ReadFile(configPath)
		if readErr != nil {
			return "", fmt.Errorf("failed to read restored mcp_config.json: %w", readErr)
		}
		sumAfter := sha256.Sum256(afterBytes)
		shaAfter := hex.EncodeToString(sumAfter[:])
		if shaAfter != shaBefore {
			return shaAfter, fmt.Errorf("mcp_config.json SHA-256 mismatch after restore: before=%s after=%s", shaBefore, shaAfter)
		}
		return shaAfter, nil
	}
	return shaBefore, restoreFn, nil
}

func (a *Adapter) RunTask(ctx context.Context, cmd protocol.CommandEnvelope) (res runtime.WorkerResult, retErr error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	runCtx := ctx
	var cancel context.CancelFunc = func() {}
	if cmd.Profile.TimeoutSeconds > 0 {
		outerTimeout := time.Duration(cmd.Profile.TimeoutSeconds+30) * time.Second
		runCtx, cancel = context.WithTimeout(ctx, outerTimeout)
	}
	defer cancel()

	if a.AutoSkipHeadlessMCP && !a.NativeWatch {
		_, restore, err := IsolateGlobalMCPConfig(runCtx, a.MCPConfigPath)
		if err != nil {
			return runtime.WorkerResult{Status: protocol.StatusFailed, Summary: err.Error()}, err
		}
		defer func() {
			if _, restoreErr := restore(); restoreErr != nil {
				res.Status = protocol.StatusFailed
				if res.Summary == "" {
					res.Summary = restoreErr.Error()
				} else {
					res.Summary = fmt.Sprintf("%s; restore error: %v", res.Summary, restoreErr)
				}
				retErr = errors.Join(retErr, restoreErr)
			}
		}()
		if a.AfterIsolateHook != nil {
			a.AfterIsolateHook(a.MCPConfigPath)
		}
	}

	isNewChat := strings.TrimSpace(cmd.WorkerSessionID) == "" && strings.TrimSpace(cmd.WorkerChat) == ""
	if resolved, err := resolveOrSyncAntigravityIDE(context.Background(), map[string]any{
		"mode":       "resolve",
		"workspace":  cmd.Workspace,
		"project":    cmd.WorkerProject,
		"chat":       cmd.WorkerChat,
		"session_id": cmd.WorkerSessionID,
	}); err != nil {
		if strings.TrimSpace(cmd.WorkerChat) != "" {
			return runtime.WorkerResult{Status: protocol.StatusFailed, Summary: err.Error()}, err
		}
	} else {
		if pid, _ := resolved["project_id"].(string); strings.TrimSpace(pid) != "" && strings.TrimSpace(cmd.WorkerProject) == "" {
			cmd.WorkerProject = strings.TrimSpace(pid)
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
		syncNative := a.SyncIDE
		if syncNative == nil {
			syncNative = func(sid string, notifyUI bool) {
				syncCtx, syncCancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer syncCancel()
				_, _ = resolveOrSyncAntigravityIDE(syncCtx, map[string]any{
					"mode":       "sync",
					"workspace":  cmd.Workspace,
					"project":    cmd.WorkerProject,
					"session_id": sid,
					"chat_title": cmd.EffectiveChatTitle(),
					"is_new":     isNewChat,
					"notify_ui":  notifyUI,
				})
			}
		}
		return a.runNative(runCtx, cmd, cli, isNewChat, syncNative)
	}
	args := a.BuildArgs(cmd)
	proc := exec.CommandContext(runCtx, cli, args...)
	if strings.TrimSpace(cmd.Workspace) != "" {
		proc.Dir = cmd.Workspace
	}
	proc.Env = append(os.Environ(), "CI=true")

	prompt := codex.BuildPrompt(cmd.Spec)
	inputLine, err := json.Marshal(map[string]any{
		"event": "user",
		"message": map[string]string{
			"content": prompt,
		},
	})
	if err != nil {
		return runtime.WorkerResult{Status: protocol.StatusFailed}, err
	}

	proc.Stdin = bytes.NewReader(append(inputLine, '\n'))
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
	syncIDE := func(sid string, notifyUI bool) {
		if sid == "" {
			return
		}
		syncTimeout := 5 * time.Second
		if notifyUI {
			syncTimeout = 45 * time.Second
		}
		syncCtx, syncCancel := context.WithTimeout(context.Background(), syncTimeout)
		defer syncCancel()
		_, _ = resolveOrSyncAntigravityIDE(syncCtx, map[string]any{
			"mode":       "sync",
			"workspace":  cmd.Workspace,
			"project":    cmd.WorkerProject,
			"session_id": sid,
			"chat_title": cmd.EffectiveChatTitle(),
			"is_new":     isNewChat,
			"notify_ui":  notifyUI,
		})
	}

	stopSync := make(chan struct{})
	var syncWG sync.WaitGroup
	syncWG.Add(1)
	go func() {
		defer syncWG.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopSync:
				return
			case <-ticker.C:
				sidMu.Lock()
				sid := liveSessionID
				sidMu.Unlock()
				syncIDE(sid, false)
			}
		}
	}()

	live := monitor.FromContext(ctx)
	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var syncedInitial bool
	for scanner.Scan() {
		line := scanner.Bytes()
		stdout.Write(line)
		stdout.WriteByte('\n')
		live.Raw(line)

		var ev rawAGYEvent
		if err := json.Unmarshal(bytes.TrimSpace(line), &ev); err == nil && ev.ConversationID != "" {
			sidMu.Lock()
			liveSessionID = ev.ConversationID
			sidMu.Unlock()
			if !syncedInitial {
				syncedInitial = true
				syncIDE(ev.ConversationID, true)
			}
		}
	}

	runErr := proc.Wait()
	close(stopSync)
	syncWG.Wait()

	res, parseErr := ParseStreamJSONL(stdout.Bytes(), stderr.Bytes(), runErr, liveSessionID)
	if res.WorkerSessionID != "" {
		syncIDE(res.WorkerSessionID, true)
	}
	return res, parseErr
}

type rawAGYEvent struct {
	Event          string `json:"event"`
	ConversationID string `json:"conversation_id,omitempty"`
	Model          string `json:"model,omitempty"`
	Result         *struct {
		ConversationID string          `json:"conversation_id"`
		Status         string          `json:"status"`
		Response       string          `json:"response"`
		NumTurns       int             `json:"num_turns"`
		Usage          *protocol.Usage `json:"usage,omitempty"`
	} `json:"result,omitempty"`
}

func ParseStreamJSONL(stdout, stderr []byte, exitErr error, fallbackSessionID string) (runtime.WorkerResult, error) {
	res := runtime.WorkerResult{
		WorkerSessionID: fallbackSessionID,
		ObservedModel:   "unknown",
	}

	stderrStr := string(stderr)
	timeoutReported := strings.Contains(strings.ToLower(stderrStr), "print timeout")

	var resultCount int
	var finalStatus string
	var finalResponse string

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev rawAGYEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.ConversationID != "" {
			res.WorkerSessionID = ev.ConversationID
		}
		if ev.Model != "" {
			res.ObservedModel = ev.Model
		}
		if ev.Event == "result" && ev.Result != nil {
			resultCount++
			if ev.Result.ConversationID != "" {
				res.WorkerSessionID = ev.Result.ConversationID
			}
			finalStatus = ev.Result.Status
			finalResponse = strings.TrimSpace(ev.Result.Response)
			res.Usage = ev.Result.Usage
		}
	}

	if exitErr != nil {
		res.Status = protocol.StatusFailed
		msg := strings.TrimSpace(stderrStr)
		if msg == "" {
			msg = exitErr.Error()
		}
		res.Summary = msg
		return res, fmt.Errorf("antigravity process failed: %w", exitErr)
	}
	if timeoutReported {
		res.Status = protocol.StatusFailed
		res.Summary = "antigravity reported print timeout on stderr"
		return res, errors.New(res.Summary)
	}
	if resultCount != 1 || finalStatus != "SUCCESS" || finalResponse == "" {
		res.Status = protocol.StatusFailed
		res.Summary = fmt.Sprintf("antigravity incomplete or failed result (count=%d status=%q empty_response=%v)",
			resultCount, finalStatus, finalResponse == "")
		return res, errors.New(res.Summary)
	}

	res.Status = protocol.StatusSucceeded
	res.Summary = finalResponse
	return res, nil
}
