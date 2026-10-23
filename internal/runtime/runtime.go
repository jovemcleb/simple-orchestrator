package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jovemcleb/simple-orchestrator/internal/monitor"
	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
	"github.com/jovemcleb/simple-orchestrator/internal/store"
)

type WorkerResult struct {
	WorkerSessionID string
	ObservedModel   string
	Status          protocol.EventStatus
	Summary         string
	Question        string
	ArtifactRefs    []string
	Usage           *protocol.Usage
}

type WorkerRunner interface {
	RunTask(ctx context.Context, cmd protocol.CommandEnvelope) (WorkerResult, error)
}

type Engine struct {
	mu                       sync.Mutex
	store                    *store.Store
	workers                  map[protocol.Provider]WorkerRunner
	activeWorkers            map[string]int
	closingRuns              map[string]bool
	inferenceCallsDuringWait int
	monitor                  *monitor.Monitor
}

func NewEngine(st *store.Store, workers map[protocol.Provider]WorkerRunner) *Engine {
	if workers == nil {
		workers = make(map[protocol.Provider]WorkerRunner)
	}
	return &Engine{
		store:         st,
		workers:       workers,
		activeWorkers: make(map[string]int),
		closingRuns:   make(map[string]bool),
	}
}

// SetMonitor enables the live terminal/log for every delegated worker (R16).
func (e *Engine) SetMonitor(m *monitor.Monitor) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.monitor = m
}

func formatUsage(u *protocol.Usage) string {
	if u == nil {
		return ""
	}
	return fmt.Sprintf("entrada=%d saída=%d raciocínio=%d cache=%d total=%d", u.InputTokens, u.OutputTokens, u.ThinkingTokens, u.CacheReadTokens, u.TotalTokens)
}

func (e *Engine) InferenceCallsDuringWait() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inferenceCallsDuringWait
}

func (e *Engine) StartOrReuse(cmd protocol.CommandEnvelope) (*store.RunSnapshot, bool, error) {
	if !protocol.ValidProvider(cmd.MaestraProvider) {
		return nil, false, fmt.Errorf("invalid maestra provider %q", cmd.MaestraProvider)
	}
	return e.store.CreateOrGetRun(cmd.RunKey, cmd.RunID, cmd.MaestraProvider, cmd.MaestraSessionID, cmd.Workspace)
}

func (e *Engine) Delegate(ctx context.Context, cmd protocol.CommandEnvelope) (protocol.Event, bool, error) {
	if err := cmd.Validate(); err != nil {
		return protocol.Event{}, false, err
	}

	e.mu.Lock()
	snap, _, err := e.store.CreateOrGetRun(cmd.RunKey, cmd.RunID, cmd.MaestraProvider, cmd.MaestraSessionID, cmd.Workspace)
	if err != nil {
		e.mu.Unlock()
		return protocol.Event{}, false, err
	}
	cmd.RunID = snap.RunID

	if e.closingRuns[cmd.RunID] || snap.State == protocol.RunCompleting || snap.State == protocol.RunCompleted {
		e.mu.Unlock()
		return protocol.Event{}, false, fmt.Errorf("run %q is completing or completed; rejecting new work", cmd.RunID)
	}

	// Check if this exact attempt already produced a terminal event (idempotency / duplicate guard).
	for _, entry := range snap.Outbox {
		if entry.Event.TaskID == cmd.TaskID && entry.Event.AttemptID == cmd.AttemptID &&
			(entry.Event.Type == protocol.EventWorkerCompleted ||
				entry.Event.Type == protocol.EventWorkerFailed ||
				entry.Event.Type == protocol.EventWorkerBlockedQuestion) {
			ev := entry.Event
			e.mu.Unlock()
			return ev, true, nil
		}
	}

	runner, ok := e.workers[cmd.WorkerProvider]
	if !ok {
		e.mu.Unlock()
		return protocol.Event{}, false, fmt.Errorf("no worker adapter registered for provider %q", cmd.WorkerProvider)
	}

	e.activeWorkers[cmd.RunID]++
	_ = e.store.UpdateRunState(cmd.RunID, protocol.RunWaiting, "")
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.activeWorkers[cmd.RunID]--
		e.mu.Unlock()
	}()

	var live *monitor.Log
	if e.monitor != nil {
		if l, err := e.monitor.Start(cmd.RunID, cmd.TaskID, cmd.AttemptID, string(cmd.WorkerProvider), cmd.EffectiveChatTitle()); err == nil {
			live = l
			ctx = monitor.NewContext(ctx, live)
		}
	}

	// Execute worker and wait via OS process/context without any LLM polling during wait (R06).
	res, runErr := runner.RunTask(ctx, cmd)
	live.Finish(string(res.Status), res.ObservedModel, res.Summary, formatUsage(res.Usage), runErr)

	ev := buildWorkerEvent(cmd, res, runErr)
	entry, duplicate, err := e.store.RecordBeforeSend(ev)
	if err != nil {
		return protocol.Event{}, false, err
	}
	if duplicate {
		return entry.Event, true, nil
	}

	// Synchronous bridge process stdout delivery: mark delivered & processed so maestra can consolidate immediately.
	_ = e.store.UpdateDeliveryState(cmd.RunID, entry.DedupeKey, protocol.DeliveryStdoutDelivered)
	if ev.Type == protocol.EventWorkerBlockedQuestion {
		_ = e.store.UpdateRunState(cmd.RunID, protocol.RunAwaitingUser, ev.Question)
	} else if ev.Status == protocol.StatusFailed {
		_ = e.store.UpdateRunState(cmd.RunID, protocol.RunFailed, "")
	} else {
		_ = e.store.UpdateRunState(cmd.RunID, protocol.RunRunning, "")
	}
	return ev, false, nil
}

func (e *Engine) Ack(runID, taskID, attemptID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	snap, err := e.store.LoadSnapshot(runID)
	if err != nil {
		return err
	}
	matched := false
	for key, entry := range snap.Outbox {
		if entry.Event.TaskID == taskID && entry.Event.AttemptID == attemptID {
			if err := e.store.UpdateDeliveryState(runID, key, protocol.DeliveryAcknowledged); err != nil {
				return err
			}
			matched = true
		}
	}
	if !matched {
		return fmt.Errorf("no outbox entry found for run=%q task=%q attempt=%q", runID, taskID, attemptID)
	}
	return nil
}

func (e *Engine) Finish(runID string) (*store.RunSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.activeWorkers[runID] > 0 {
		return nil, fmt.Errorf("cannot finish run %q: %d worker(s) still active", runID, e.activeWorkers[runID])
	}
	snap, err := e.store.LoadSnapshot(runID)
	if err != nil {
		return nil, err
	}
	if snap.State == protocol.RunAwaitingUser || snap.PendingQuestion != "" {
		return nil, fmt.Errorf("cannot finish run %q: blocking question pending user/maestra answer", runID)
	}
	for _, entry := range snap.Outbox {
		if entry.DeliveryState == protocol.DeliveryUnknown {
			return nil, fmt.Errorf("cannot finish run %q: delivery %q is in ambiguous state unknown", runID, entry.DedupeKey)
		}
		if entry.DeliveryState != protocol.DeliveryAcknowledged {
			return nil, fmt.Errorf("cannot finish run %q: delivery %q is %q (must be acknowledged)", runID, entry.DedupeKey, entry.DeliveryState)
		}
	}

	e.closingRuns[runID] = true
	if err := e.store.UpdateRunState(runID, protocol.RunCompleting, ""); err != nil {
		return nil, err
	}
	if err := e.store.UpdateRunState(runID, protocol.RunCompleted, ""); err != nil {
		return nil, err
	}
	return e.store.LoadSnapshot(runID)
}

func buildWorkerEvent(cmd protocol.CommandEnvelope, res WorkerResult, runErr error) protocol.Event {
	sessionID := res.WorkerSessionID
	if sessionID == "" {
		sessionID = cmd.WorkerSessionID
	}
	if sessionID == "" {
		sessionID = fmt.Sprintf("worker-%s-%s", cmd.TaskID, cmd.AttemptID)
	}
	reqModel := cmd.Profile.RequestedModel
	if reqModel == "" {
		reqModel = "default"
	}
	obsModel := res.ObservedModel
	if obsModel == "" {
		obsModel = cmd.Profile.ObservedModel
	}
	if obsModel == "" {
		obsModel = "unknown"
	}

	ev := protocol.Event{
		EventID:              fmt.Sprintf("evt-%s-%s-%d", cmd.TaskID, cmd.AttemptID, time.Now().UTC().UnixNano()),
		RunID:                cmd.RunID,
		TaskID:               cmd.TaskID,
		AttemptID:            cmd.AttemptID,
		SourceProvider:       cmd.WorkerProvider,
		SourceSessionID:      sessionID,
		DestinationProvider:  cmd.MaestraProvider,
		DestinationSessionID: cmd.MaestraSessionID,
		RequestedModel:       reqModel,
		ObservedModel:        obsModel,
		ArtifactRefs:         res.ArtifactRefs,
		OccurredAt:           time.Now().UTC(),
		Usage:                res.Usage,
	}

	if runErr != nil || res.Status == protocol.StatusFailed {
		ev.Type = protocol.EventWorkerFailed
		ev.Status = protocol.StatusFailed
		summary := res.Summary
		if summary == "" && runErr != nil {
			summary = runErr.Error()
		}
		if summary == "" {
			summary = "worker execution failed"
		}
		ev.Summary = summary
		return ev
	}

	if res.Status == protocol.StatusBlocked || res.Question != "" {
		ev.Type = protocol.EventWorkerBlockedQuestion
		ev.Status = protocol.StatusBlocked
		ev.Question = res.Question
		ev.Summary = res.Summary
		if ev.Summary == "" {
			ev.Summary = res.Question
		}
		return ev
	}

	ev.Type = protocol.EventWorkerCompleted
	ev.Status = protocol.StatusSucceeded
	ev.Summary = res.Summary
	if ev.Summary == "" {
		ev.Type = protocol.EventWorkerFailed
		ev.Status = protocol.StatusFailed
		ev.Summary = errors.New("empty worker summary is not valid completion").Error()
	}
	return ev
}
