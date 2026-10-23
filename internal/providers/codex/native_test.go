package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
)

var reNonce = regexp.MustCompile(`myo-attempt-[^\]]+`)

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func rolloutLine(t time.Time, typ, payload string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":%q,"payload":%s}`, ts(t), typ, payload)
}

// goodTurn is a completed first turn recorded after the bridge started.
func goodTurn(nonce string) string {
	now := time.Now()
	return strings.Join([]string{
		rolloutLine(now, "session_meta", `{"id":"sess-1"}`),
		rolloutLine(now, "response_item", `{"type":"message","text":"`+nonce+`"}`),
		rolloutLine(now, "turn_context", `{"model":"gpt-6.1-sol"}`),
		rolloutLine(now, "event_msg", `{"type":"task_started"}`),
		rolloutLine(now, "event_msg", `{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":10,"reasoning_output_tokens":4,"total_tokens":110}}}`),
		rolloutLine(now, "event_msg", `{"type":"task_complete","last_agent_message":"NATIVE_OK"}`),
	}, "\n") + "\n"
}

type fakeTUI struct {
	rollout   func(nonce string) string // "" = Codex never writes a session file
	exitEarly bool                      // window closed by the user
	killed    atomic.Bool
	argv      []string
}

func (f *fakeTUI) launcher(sessionsDir string) NativeLauncher {
	return func(dir string, argv []string) (<-chan struct{}, func(), error) {
		f.argv = argv
		exited := make(chan struct{})
		go func() {
			if f.rollout != nil {
				if c := f.rollout(reNonce.FindString(argv[len(argv)-1])); c != "" {
					_ = os.MkdirAll(sessionsDir, 0o755)
					_ = os.WriteFile(filepath.Join(sessionsDir, "rollout-x-sess-1.jsonl"), []byte(c), 0o644)
				}
			}
			if f.exitEarly {
				close(exited)
			}
			// otherwise the TUI keeps running after the turn, like the real one
		}()
		return exited, func() { f.killed.Store(true) }, nil
	}
}

func nativeCmd() protocol.CommandEnvelope {
	return protocol.CommandEnvelope{
		RunID: "run-n", TaskID: "task-1", AttemptID: "att-1",
		Spec: protocol.TaskSpec{Target: "x", Change: `ler "a" & b | c`, Acceptance: "NATIVE_OK"},
	}
}

func newNative(t *testing.T, f *fakeTUI) *Adapter {
	t.Helper()
	a := NewAdapter(`C:\fake\codex.exe`)
	a.NativeWatch = true
	a.SessionsDir = filepath.Join(t.TempDir(), "sessions")
	a.Launch = f.launcher(a.SessionsDir)
	a.PollInterval = 20 * time.Millisecond
	return a
}

func TestRunNative_ReturnsOnFirstTurnWhileWindowStaysOpen(t *testing.T) {
	f := &fakeTUI{rollout: goodTurn}
	a := newNative(t, f)
	res, err := a.runNative(context.Background(), nativeCmd(), a.CLIPath, "", true)
	if err != nil || res.Status != protocol.StatusSucceeded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.WorkerSessionID != "sess-1" || res.ObservedModel != "gpt-6.1-sol" || res.Summary != "NATIVE_OK" {
		t.Errorf("unexpected result: %+v", res)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 110 || res.Usage.CacheReadTokens != 40 || res.Usage.ThinkingTokens != 4 {
		t.Errorf("usage: %+v", res.Usage)
	}
	if f.killed.Load() {
		t.Error("window must stay open after a successful turn")
	}
	if last := f.argv[len(f.argv)-1]; !strings.Contains(last, `ler "a" & b | c`) || !strings.Contains(last, "\n") {
		t.Errorf("prompt must be passed intact as one argument: %q", last)
	}
}

func TestRunNative_ErrorOrAbortIsFailure(t *testing.T) {
	for name, tail := range map[string]string{
		"error":   `{"type":"error","message":"quota exceeded"}`,
		"aborted": `{"type":"turn_aborted"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeTUI{rollout: func(nonce string) string {
				now := time.Now()
				return rolloutLine(now, "session_meta", `{"id":"sess-1"}`) + "\n" +
					rolloutLine(now, "response_item", `{"text":"`+nonce+`"}`) + "\n" +
					rolloutLine(now, "event_msg", `{"type":"task_started"}`) + "\n" +
					rolloutLine(now, "event_msg", tail) + "\n"
			}}
			res, err := newNative(t, f).runNative(context.Background(), nativeCmd(), `C:\fake\codex.exe`, "", true)
			if err == nil || res.Status != protocol.StatusFailed || !strings.Contains(res.Summary, "não concluiu") {
				t.Fatalf("res=%+v err=%v", res, err)
			}
		})
	}
}

func TestRunNative_OldTurnsOfResumedSessionAreIgnored(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	f := &fakeTUI{rollout: func(string) string {
		return rolloutLine(old, "session_meta", `{"id":"sess-9"}`) + "\n" +
			rolloutLine(old, "event_msg", `{"type":"task_complete","last_agent_message":"antigo"}`) + "\n" +
			rolloutLine(old, "event_msg", `{"type":"turn_aborted"}`) + "\n"
	}}
	a := newNative(t, f)
	a.SessionsDir = filepath.Join(t.TempDir(), "s")
	a.Launch = func(dir string, argv []string) (<-chan struct{}, func(), error) {
		_ = os.MkdirAll(a.SessionsDir, 0o755)
		_ = os.WriteFile(filepath.Join(a.SessionsDir, "rollout-2026-sess-9.jsonl"), []byte(f.rollout("")), 0o644)
		go func() {
			time.Sleep(100 * time.Millisecond)
			now := time.Now()
			fh, _ := os.OpenFile(filepath.Join(a.SessionsDir, "rollout-2026-sess-9.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
			fmt.Fprintln(fh, rolloutLine(now, "event_msg", `{"type":"task_started"}`))
			fmt.Fprintln(fh, rolloutLine(now, "event_msg", `{"type":"task_complete","last_agent_message":"novo"}`))
			fh.Close()
		}()
		return make(chan struct{}), func() {}, nil
	}
	cmd := nativeCmd()
	cmd.WorkerSessionID = "sess-9"
	res, err := a.runNative(context.Background(), cmd, `C:\fake\codex.exe`, "", false)
	if err != nil || res.Summary != "novo" || res.Usage != nil {
		t.Fatalf("expected only the new turn to count, res=%+v err=%v", res, err)
	}
}

func TestRunNative_WindowClosedBeforeTurnEnds(t *testing.T) {
	f := &fakeTUI{exitEarly: true}
	res, err := newNative(t, f).runNative(context.Background(), nativeCmd(), `C:\fake\codex.exe`, "", true)
	if err == nil || !strings.Contains(res.Summary, "fechada antes") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRunNative_TimeoutKillsWindow(t *testing.T) {
	f := &fakeTUI{}
	a := newNative(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := a.runNative(ctx, nativeCmd(), `C:\fake\codex.exe`, "", true); err == nil {
		t.Fatal("expected timeout failure")
	}
	if !f.killed.Load() {
		t.Error("timed-out window must be killed")
	}
}

func TestBuildInteractiveArgs(t *testing.T) {
	cmd := nativeCmd()
	cmd.Profile = protocol.Profile{RequestedModel: "gpt-6.1-sol", Effort: "low", ReadOnly: true}
	got := buildInteractiveArgs(cmd, `C:\w s`, "P")
	j := strings.Join(got, " ")
	if strings.Contains(j, "exec") || strings.Contains(j, "--json") || got[len(got)-1] != "P" ||
		!strings.Contains(j, "--sandbox read-only") || !strings.Contains(j, "--ask-for-approval never") || !strings.Contains(j, `--cd C:\w s`) {
		t.Errorf("new: %q", j)
	}
	cmd.WorkerSessionID = "sess-9"
	got = buildInteractiveArgs(cmd, "", "P")
	if got[0] != "resume" || got[len(got)-2] != "sess-9" || got[len(got)-1] != "P" {
		t.Errorf("resume: %q", got)
	}
}

func TestResolveInteractive(t *testing.T) {
	if got, err := resolveInteractive(`C:\x\codex.exe`); err != nil || len(got) != 1 {
		t.Errorf("exe: %v %v", got, err)
	}
	dir := t.TempDir()
	js := filepath.Join(dir, "node_modules", "@openai", "codex", "bin", "codex.js")
	_ = os.MkdirAll(filepath.Dir(js), 0o755)
	_ = os.WriteFile(js, nil, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "node.exe"), nil, 0o644)
	got, err := resolveInteractive(filepath.Join(dir, "codex.cmd"))
	if err != nil || got[0] != filepath.Join(dir, "node.exe") || got[1] != js {
		t.Errorf("cmd shim: %v %v", got, err)
	}
	if _, err := resolveInteractive(filepath.Join(t.TempDir(), "codex.cmd")); err == nil {
		t.Error("missing codex.js must fail")
	}
}
