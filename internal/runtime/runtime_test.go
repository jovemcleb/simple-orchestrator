package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
	"github.com/jovemcleb/simple-orchestrator/internal/store"
)

type fakeWorker struct {
	delay  time.Duration
	result WorkerResult
	err    error
	calls  int
}

func (f *fakeWorker) RunTask(ctx context.Context, cmd protocol.CommandEnvelope) (WorkerResult, error) {
	f.calls++
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return WorkerResult{}, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	return f.result, f.err
}

func sampleDelegateCmd(runID, taskID, attemptID string) protocol.CommandEnvelope {
	return protocol.CommandEnvelope{
		Action:           protocol.ActionDelegate,
		RunKey:           "key-" + runID,
		RunID:            runID,
		TaskID:           taskID,
		AttemptID:        attemptID,
		MaestraProvider:  protocol.ProviderCodex,
		MaestraSessionID: "01a15543-4df3-78b3-853e-3dd07a139b28",
		WorkerProvider:   protocol.ProviderAntigravity,
		Workspace:        `C:\Users\Example User\projects\sample app`,
		Spec: protocol.TaskSpec{
			Target:      "docs/spikes/native-return.md",
			Change:      "Read-only check",
			Constraints: "No writes",
			Ownership:   "worker",
			Acceptance:  "MYO_T01_WORKER_OK",
			Prompt:      "Respond MYO_T01_WORKER_OK",
		},
	}
}

func TestEngine_LifecycleAndZeroInferenceWait(t *testing.T) {
	t.Parallel()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New error = %v", err)
	}
	fw := &fakeWorker{
		result: WorkerResult{
			WorkerSessionID: "b2ffec5f-bc52-4990-8657-031faa8ba2a9",
			ObservedModel:   "Gemini 3.8 Flash (High)",
			Status:          protocol.StatusSucceeded,
			Summary:         "MYO_T01_WORKER_OK",
			Usage:           &protocol.Usage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110},
		},
	}
	eng := NewEngine(st, map[protocol.Provider]WorkerRunner{
		protocol.ProviderAntigravity: fw,
	})

	cmd := sampleDelegateCmd("run-1", "task-1", "att-1")
	ev, dup, err := eng.Delegate(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Delegate error = %v", err)
	}
	if dup {
		t.Fatalf("expected dup=false on first delegate")
	}
	if ev.Status != protocol.StatusSucceeded || ev.Summary != "MYO_T01_WORKER_OK" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if eng.InferenceCallsDuringWait() != 0 {
		t.Fatalf("expected 0 inference calls during wait, got %d", eng.InferenceCallsDuringWait())
	}

	// Duplicate call with same task/attempt does not re-run the worker LLM.
	ev2, dup2, err := eng.Delegate(context.Background(), cmd)
	if err != nil {
		t.Fatalf("second Delegate error = %v", err)
	}
	if !dup2 || fw.calls != 1 || ev2.EventID != ev.EventID {
		t.Fatalf("expected deduplicated result without second worker call, calls=%d dup2=%v", fw.calls, dup2)
	}

	// Finish before Ack must fail (R10).
	if _, err := eng.Finish("run-1"); err == nil {
		t.Fatalf("expected Finish before Ack to fail")
	}

	if err := eng.Ack("run-1", "task-1", "att-1"); err != nil {
		t.Fatalf("Ack error = %v", err)
	}
	snap, err := eng.Finish("run-1")
	if err != nil {
		t.Fatalf("Finish after Ack error = %v", err)
	}
	if snap.State != protocol.RunCompleted {
		t.Fatalf("expected RunCompleted, got %q", snap.State)
	}

	// New work arriving after Finish must be clearly rejected under lock.
	cmd2 := sampleDelegateCmd("run-1", "task-2", "att-1")
	if _, _, err := eng.Delegate(context.Background(), cmd2); err == nil || !strings.Contains(err.Error(), "completing or completed") {
		t.Fatalf("expected rejection on completed run, got err=%v", err)
	}
}

func TestEngine_BlockingQuestionPreventsFinish(t *testing.T) {
	t.Parallel()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New error = %v", err)
	}
	fw := &fakeWorker{
		result: WorkerResult{
			WorkerSessionID: "sess-agy-q",
			Status:          protocol.StatusBlocked,
			Summary:         "Need schema clarification",
			Question:        "Which table holds historical IE records?",
		},
	}
	eng := NewEngine(st, map[protocol.Provider]WorkerRunner{
		protocol.ProviderAntigravity: fw,
	})

	cmd := sampleDelegateCmd("run-q", "task-q", "att-1")
	ev, _, err := eng.Delegate(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Delegate error = %v", err)
	}
	if ev.Type != protocol.EventWorkerBlockedQuestion {
		t.Fatalf("expected EventWorkerBlockedQuestion, got %q", ev.Type)
	}
	if err := eng.Ack("run-q", "task-q", "att-1"); err != nil {
		t.Fatalf("Ack error = %v", err)
	}
	if _, err := eng.Finish("run-q"); err == nil || !strings.Contains(err.Error(), "blocking question") {
		t.Fatalf("expected Finish blocked by pending question, got %v", err)
	}
}
