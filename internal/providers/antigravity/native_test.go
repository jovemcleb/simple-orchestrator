package antigravity

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
)

type scriptedProbe struct {
	mu     sync.Mutex
	states []agyTurnState
	calls  int
	nonce  string
}

func (p *scriptedProbe) probe(_ context.Context, nonce string, _ time.Time, _ string) (agyTurnState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nonce = nonce
	i := p.calls
	if i >= len(p.states) {
		i = len(p.states) - 1
	}
	p.calls++
	return p.states[i], nil
}

func running() agyTurnState {
	return agyTurnState{ConversationID: "conv-1", FoundInput: true, Status: "CASCADE_RUN_STATUS_RUNNING", NotFullyIdle: true}
}

func idle(steps ...agyStep) agyTurnState {
	return agyTurnState{ConversationID: "conv-1", FoundInput: true, Status: "CASCADE_RUN_STATUS_IDLE", Steps: steps}
}

func finalStep(text string) agyStep {
	return agyStep{Type: "PLANNER_RESPONSE", Status: "DONE", Content: text, InputTokens: 100, OutputTokens: 5, CacheReadTokens: 50}
}

type nativeHarness struct {
	a       *Adapter
	argv    []string
	killed  atomic.Bool
	synced  []string
	exitNow bool
}

func newHarness(states ...agyTurnState) (*nativeHarness, *scriptedProbe) {
	h := &nativeHarness{a: NewAdapter(`C:\agy.exe`, "", true)}
	p := &scriptedProbe{states: states}
	h.a.NativeWatch = true
	h.a.PollInterval = 10 * time.Millisecond
	h.a.NoFinalGrace = 40 * time.Millisecond
	h.a.ConversationOpen = func(string) bool { return false }
	h.a.Probe = p.probe
	h.a.SyncIDE = func(sid string, _ bool) { h.synced = append(h.synced, sid) }
	h.a.Launch = func(dir string, argv []string) (<-chan struct{}, func(), error) {
		h.argv = argv
		ch := make(chan struct{})
		if h.exitNow {
			close(ch)
		}
		return ch, func() { h.killed.Store(true) }, nil
	}
	return h, p
}

func nativeCmd() protocol.CommandEnvelope {
	return protocol.CommandEnvelope{
		RunID: "r", TaskID: "t", AttemptID: "a", Workspace: `C:\w`,
		Profile: protocol.Profile{RequestedModel: "gemini-3.8-flash-high", Effort: "low"},
		Spec:    protocol.TaskSpec{Target: "x", Change: `ler "a" & b`, Acceptance: "OK"},
	}
}

func TestRunNative_SucceedsWhenIdleWithFinalAnswer(t *testing.T) {
	h, p := newHarness(agyTurnState{}, running(), idle(agyStep{Type: "PLANNER_RESPONSE", Status: "DONE", ToolCalls: 1, InputTokens: 10}, finalStep("MYO_OK")))
	res, err := h.a.RunTask(context.Background(), nativeCmd())
	if err != nil || res.Status != protocol.StatusSucceeded || res.Summary != "MYO_OK" || res.WorkerSessionID != "conv-1" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.Usage == nil || res.Usage.InputTokens != 110 || res.Usage.CacheReadTokens != 50 || res.Usage.TotalTokens != 115 {
		t.Errorf("usage: %+v", res.Usage)
	}
	if h.killed.Load() {
		t.Error("window must stay open after success")
	}
	last := h.argv[len(h.argv)-1]
	if h.argv[len(h.argv)-2] != "-i" || !strings.Contains(last, `ler "a" & b`) || !strings.Contains(last, p.nonce) {
		t.Errorf("argv: %q", h.argv)
	}
	if strings.Contains(strings.Join(h.argv, " "), "stream-json") {
		t.Error("interactive mode must not use print/stream-json flags")
	}
	if len(h.synced) == 0 || h.synced[0] != "conv-1" {
		t.Errorf("UI sync not triggered: %v", h.synced)
	}
}

func TestRunNative_NotDoneWhileNotFullyIdleOrToolCallPending(t *testing.T) {
	notFully := idle(finalStep("parcial"))
	notFully.NotFullyIdle = true
	cases := []agyTurnState{
		running(),
		notFully,
		{ConversationID: "c", FoundInput: true, Status: "CASCADE_RUN_STATUS_IDLE", Steps: []agyStep{{Type: "GENERIC", Status: "RUNNING"}}},
		{ConversationID: "c", FoundInput: false, Status: "CASCADE_RUN_STATUS_IDLE", Steps: []agyStep{finalStep("x")}},
	}
	for i, st := range cases {
		if done, _, _ := turnOutcome(st); done {
			t.Errorf("case %d must not be done: %+v", i, st)
		}
	}
}

func TestRunNative_FailureCases(t *testing.T) {
	cases := map[string]agyTurnState{
		"error step":     idle(agyStep{Type: "GENERIC", Status: "ERROR", Content: "quota exceeded"}),
		"no final":       idle(agyStep{Type: "PLANNER_RESPONSE", Status: "DONE", ToolCalls: 2}),
		"killed":         {ConversationID: "c", Killed: true},
		"empty response": idle(finalStep("   ")),
	}
	for name, st := range cases {
		t.Run(name, func(t *testing.T) {
			h, _ := newHarness(st)
			res, err := h.a.RunTask(context.Background(), nativeCmd())
			if err == nil || res.Status != protocol.StatusFailed || !strings.Contains(res.Summary, "não concluiu") {
				t.Fatalf("res=%+v err=%v", res, err)
			}
		})
	}
}

func TestRunNative_WindowClosedAndTimeout(t *testing.T) {
	h, _ := newHarness(running())
	h.exitNow = true
	res, err := h.a.RunTask(context.Background(), nativeCmd())
	if err == nil || !strings.Contains(res.Summary, "fechada antes") {
		t.Fatalf("closed: res=%+v err=%v", res, err)
	}

	h2, _ := newHarness(running())
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if _, err := h2.a.RunTask(ctx, nativeCmd()); err == nil || !h2.killed.Load() {
		t.Fatalf("timeout must fail and kill the window, err=%v killed=%v", err, h2.killed.Load())
	}
}

func TestRunNative_DoesNotIsolateGlobalMCP(t *testing.T) {
	h, _ := newHarness(idle(finalStep("ok")))
	called := false
	h.a.AfterIsolateHook = func(string) { called = true }
	if _, err := h.a.RunTask(context.Background(), nativeCmd()); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("interactive mode must not isolate the global MCP config")
	}
}

func TestBuildInteractiveArgs_Resume(t *testing.T) {
	a := NewAdapter(`C:\agy.exe`, "", true)
	cmd := nativeCmd()
	cmd.WorkerSessionID = "conv-9"
	cmd.WorkerProject = "proj"
	j := strings.Join(a.buildInteractiveArgs(cmd, "P"), " ")
	for _, want := range []string{"--conversation conv-9", "--project proj", "--mode auto", "--dangerously-skip-permissions", "--effort low", "-i P"} {
		if !strings.Contains(j, want) {
			t.Errorf("missing %q in %q", want, j)
		}
	}
}

// Observed in a real run: a SYSTEM_MESSAGE is written after the prompt while the
// summary still says IDLE, before the model answers. That must not end the turn.
func TestRunNative_SystemStepBeforeAnswerIsNotTheEnd(t *testing.T) {
	sys := agyStep{Type: "SYSTEM_MESSAGE", Status: "DONE", Content: "<SYSTEM_MESSAGE>"}
	h, _ := newHarness(idle(sys), idle(sys), idle(sys, finalStep("MYO_OK")))
	h.a.NoFinalGrace = time.Second
	res, err := h.a.RunTask(context.Background(), nativeCmd())
	if err != nil || res.Summary != "MYO_OK" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRunNative_IdleWithoutAnswerFailsOnlyAfterGrace(t *testing.T) {
	h, p := newHarness(idle(agyStep{Type: "SYSTEM_MESSAGE", Status: "DONE"}))
	res, err := h.a.RunTask(context.Background(), nativeCmd())
	if err == nil || !strings.Contains(res.Summary, "sem resposta final") || p.calls < 3 {
		t.Fatalf("res=%+v err=%v calls=%d", res, err, p.calls)
	}
}

func TestRunNative_ResumeRefusedWhenConversationOpenInAnotherAgy(t *testing.T) {
	h, _ := newHarness(idle(finalStep("x")))
	h.a.ConversationOpen = func(string) bool { return true }
	cmd := nativeCmd()
	cmd.WorkerSessionID = "conv-open"
	res, err := h.a.RunTask(context.Background(), cmd)
	if err == nil || h.argv != nil || !strings.Contains(res.Summary, "já está aberta") {
		t.Fatalf("res=%+v err=%v argv=%v", res, err, h.argv)
	}
}
