package copilot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
)

func evLine(typ string, data string) string {
	return fmt.Sprintf(`{"type":%q,"timestamp":%q,"data":%s}`, typ, time.Now().UTC().Format(time.RFC3339Nano), data)
}

func cmdWith(model, effort string) protocol.CommandEnvelope {
	return protocol.CommandEnvelope{
		RunID: "r", TaskID: "t", AttemptID: "a", Workspace: `C:\w`,
		Profile: protocol.Profile{RequestedModel: model, Effort: effort},
		Spec:    protocol.TaskSpec{Target: "x", Change: `ler "a" & b`, Acceptance: "OK"},
	}
}

func goodTurn(model string) []string {
	return []string{
		evLine("session.start", `{"selectedModel":"`+model+`","reasoningEffort":"low"}`),
		evLine("session.model_change", `{"newModel":"`+model+`","previousModel":"`+model+`"}`),
		evLine("user.message", `{"content":"prompt"}`),
		evLine("assistant.turn_start", `{"turnId":"0"}`),
		evLine("session.usage_record", `{"usage":{"model":"`+model+`","inputTokens":100,"outputTokens":10,"cacheReadTokens":40,"reasoningTokens":3}}`),
		evLine("assistant.message", `{"model":"`+model+`","content":"vou ler","toolRequests":[{"name":"view"}]}`),
		evLine("tool.execution_start", `{"toolName":"view"}`),
		evLine("assistant.turn_end", `{"turnId":"0"}`),
		evLine("assistant.turn_start", `{"turnId":"1"}`),
		evLine("session.usage_record", `{"usage":{"model":"`+model+`","inputTokens":50,"outputTokens":5,"cacheReadTokens":0,"reasoningTokens":1}}`),
		evLine("assistant.message", `{"model":"`+model+`","content":"MYO_OK","toolRequests":[]}`),
		evLine("assistant.turn_end", `{"turnId":"1"}`),
	}
}

func track(lines []string, model string) *tracker {
	tr := newTracker(model, time.Now().Add(-time.Minute))
	for _, l := range lines {
		tr.feed([]byte(l))
	}
	return tr
}

func TestTracker_DoneOnlyAfterFinalAnswerWithoutTools(t *testing.T) {
	lines := goodTurn("gpt-5.6-luna")
	if tr := track(lines[:8], "gpt-5.6-luna"); tr.decided() {
		t.Fatal("turn_end after a tool request must not finish the task")
	}
	tr := track(lines, "gpt-5.6-luna")
	res, err := tr.result("sid")
	if err != nil || res.Summary != "MYO_OK" || res.ObservedModel != "gpt-5.6-luna" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if u := res.Usage; u == nil || u.InputTokens != 150 || u.OutputTokens != 15 || u.CacheReadTokens != 40 || u.ThinkingTokens != 4 || u.TotalTokens != 165 {
		t.Errorf("usage: %+v", res.Usage)
	}
}

func TestTracker_SilentModelSwitchFails(t *testing.T) {
	cases := map[string][]string{
		"error model": {evLine("session.error", `{"message":"Model \"x\" from --model flag is not available. Using \"auto\" instead.","errorType":"model"}`)},
		"change auto": {evLine("session.model_change", `{"newModel":"auto","previousModel":"gpt-5.6-luna"}`)},
		"other model": {evLine("user.message", `{}`), evLine("assistant.message", `{"model":"claude-haiku-5.5","content":"oi","toolRequests":[]}`), evLine("assistant.turn_end", `{}`)},
	}
	for name, lines := range cases {
		t.Run(name, func(t *testing.T) {
			tr := track(lines, "gpt-5.6-luna")
			res, err := tr.result("sid")
			if err == nil || !tr.modelSwitched || res.Status != protocol.StatusFailed {
				t.Fatalf("res=%+v err=%v", res, err)
			}
		})
	}
}

func TestTracker_IgnoresOlderTurnsAndFailsOnSessionError(t *testing.T) {
	old := fmt.Sprintf(`{"type":"assistant.turn_end","timestamp":%q,"data":{}}`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano))
	tr := newTracker("m", time.Now())
	tr.feed([]byte(`{"type":"user.message","timestamp":"` + time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano) + `","data":{}}`))
	tr.feed([]byte(old))
	if tr.decided() {
		t.Fatal("older events must be ignored")
	}
	tr2 := track([]string{evLine("session.error", `{"message":"rate limited","errorType":"rate_limit"}`)}, "m")
	if _, err := tr2.result("s"); err == nil || tr2.modelSwitched {
		t.Fatal("session error must fail without being a model switch")
	}
}

func TestTracker_HeadlessResultExitCode(t *testing.T) {
	tr := newTracker("m", time.Time{})
	tr.feed([]byte(`{"type":"result","exitCode":1}`))
	if _, err := tr.result("s"); err == nil {
		t.Fatal("non-zero exit must fail")
	}
}

func TestRequireModelAndEffort(t *testing.T) {
	for _, c := range []struct{ m, e string }{{"", "low"}, {"default", "low"}, {"auto", "low"}, {"gpt-5.6-luna", ""}} {
		if _, _, err := requireModelAndEffort(cmdWith(c.m, c.e)); err == nil {
			t.Errorf("model=%q effort=%q must be rejected", c.m, c.e)
		}
	}
	if m, e, err := requireModelAndEffort(cmdWith("gpt-5.6-luna", "low")); err != nil || m != "gpt-5.6-luna" || e != "low" {
		t.Errorf("valid pair rejected: %s %s %v", m, e, err)
	}
}

func TestBuildArgs(t *testing.T) {
	c := cmdWith("gpt-5.6-luna", "low")
	c.Profile.ReadOnly = true
	j := strings.Join(buildArgs(c, "gpt-5.6-luna", "low", "sid-1", false), " ")
	for _, want := range []string{"--model gpt-5.6-luna", "--reasoning-effort low", "--session-id sid-1", "-n ", "--allow-all-tools", "--deny-tool write"} {
		if !strings.Contains(j, want) {
			t.Errorf("missing %q in %q", want, j)
		}
	}
	j = strings.Join(buildArgs(c, "m", "low", "sid-9", true), " ")
	if !strings.Contains(j, "--resume sid-9") || strings.Contains(j, "--session-id") {
		t.Errorf("resume args: %q", j)
	}
}

type fakeWindow struct {
	killed atomic.Bool
	argv   []string
}

func newNativeAdapter(t *testing.T, w *fakeWindow, write func(dir string, sid string), exitNow bool) *Adapter {
	t.Helper()
	a := NewAdapter(`C:\copilot.exe`)
	a.NativeWatch = true
	a.StateDir = t.TempDir()
	a.PollInterval = 10 * time.Millisecond
	a.Launch = func(dir string, argv []string) (<-chan struct{}, func(), error) {
		w.argv = argv
		ch := make(chan struct{})
		var sid string
		for i, s := range argv {
			if s == "--session-id" || s == "--resume" {
				sid = argv[i+1]
			}
		}
		go func() {
			if write != nil {
				write(a.StateDir, sid)
			}
			if exitNow {
				close(ch)
			}
		}()
		return ch, func() { w.killed.Store(true) }, nil
	}
	return a
}

func writeEvents(lines []string) func(string, string) {
	return func(dir, sid string) {
		_ = os.MkdirAll(filepath.Join(dir, sid), 0o755)
		_ = os.WriteFile(filepath.Join(dir, sid, "events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
}

func TestRunNative_Success(t *testing.T) {
	w := &fakeWindow{}
	a := newNativeAdapter(t, w, writeEvents(goodTurn("gpt-5.6-luna")), false)
	res, err := a.RunTask(context.Background(), cmdWith("gpt-5.6-luna", "low"))
	if err != nil || res.Status != protocol.StatusSucceeded || res.Summary != "MYO_OK" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if w.killed.Load() {
		t.Error("window must stay open after success")
	}
	if w.argv[len(w.argv)-2] != "-i" || !strings.Contains(w.argv[len(w.argv)-1], `ler "a" & b`) {
		t.Errorf("argv: %q", w.argv)
	}
}

func TestRunNative_ModelFallbackKillsWindow(t *testing.T) {
	w := &fakeWindow{}
	a := newNativeAdapter(t, w, writeEvents([]string{
		evLine("session.start", `{"selectedModel":"gpt-5.6-luna"}`),
		evLine("session.error", `{"message":"Using \"auto\" instead.","errorType":"model"}`),
	}), false)
	res, err := a.RunTask(context.Background(), cmdWith("gpt-5.6-luna", "low"))
	if err == nil || !strings.Contains(res.Summary, "modelo indisponível") || !w.killed.Load() {
		t.Fatalf("res=%+v err=%v killed=%v", res, err, w.killed.Load())
	}
}

func TestRunNative_ClosedWindowAndTimeout(t *testing.T) {
	w := &fakeWindow{}
	a := newNativeAdapter(t, w, nil, true)
	if res, err := a.RunTask(context.Background(), cmdWith("m", "low")); err == nil || !strings.Contains(res.Summary, "fechada antes") {
		t.Fatalf("closed: %+v %v", res, err)
	}
	w2 := &fakeWindow{}
	b := newNativeAdapter(t, w2, nil, false)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if _, err := b.RunTask(ctx, cmdWith("m", "low")); err == nil || !w2.killed.Load() {
		t.Fatalf("timeout must fail and kill: err=%v", err)
	}
}

func TestRunTask_RejectsMissingModelBeforeLaunching(t *testing.T) {
	w := &fakeWindow{}
	a := newNativeAdapter(t, w, nil, false)
	if _, err := a.RunTask(context.Background(), cmdWith("auto", "low")); err == nil || w.argv != nil {
		t.Fatalf("must fail before launching, err=%v argv=%v", err, w.argv)
	}
}
