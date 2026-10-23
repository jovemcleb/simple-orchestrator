package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
)

func TestStore_CreateOrGetRun_IdempotentByKey(t *testing.T) {
	t.Parallel()
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, reused1, err := st.CreateOrGetRun("key-pes02", "run-100", protocol.ProviderCodex, "sess-codex-1", `C:\Users\Example User\projects\sample app`)
	if err != nil {
		t.Fatalf("first CreateOrGetRun error = %v", err)
	}
	if reused1 {
		t.Fatalf("expected reused1=false on first creation")
	}

	second, reused2, err := st.CreateOrGetRun("key-pes02", "run-999-ignored", protocol.ProviderCodex, "sess-codex-1", `C:\Users\Example User\projects\sample app`)
	if err != nil {
		t.Fatalf("second CreateOrGetRun error = %v", err)
	}
	if !reused2 {
		t.Fatalf("expected reused2=true on duplicate key")
	}
	if first.RunID != second.RunID {
		t.Fatalf("expected same RunID, got %q vs %q", first.RunID, second.RunID)
	}
}

func TestStore_RecordBeforeSend_DeduplicationAndRecovery(t *testing.T) {
	t.Parallel()
	baseDir := t.TempDir()
	st, err := New(baseDir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	snap, _, err := st.CreateOrGetRun("key-1", "run-1", protocol.ProviderCodex, "sess-codex-1", `C:\workspace`)
	if err != nil {
		t.Fatalf("CreateOrGetRun error = %v", err)
	}

	ev := protocol.Event{
		EventID:              "evt-1",
		RunID:                snap.RunID,
		TaskID:               "task-1",
		AttemptID:            "att-1",
		SourceProvider:       protocol.ProviderAntigravity,
		SourceSessionID:      "sess-agy-1",
		DestinationProvider:  protocol.ProviderCodex,
		DestinationSessionID: "sess-codex-1",
		Type:                 protocol.EventWorkerCompleted,
		Status:               protocol.StatusSucceeded,
		Summary:              "MYO_T01_WORKER_OK",
		OccurredAt:           time.Now().UTC(),
	}

	entry1, dup1, err := st.RecordBeforeSend(ev)
	if err != nil {
		t.Fatalf("RecordBeforeSend(1) error = %v", err)
	}
	if dup1 || entry1.DeliveryState != protocol.DeliveryPersisted {
		t.Fatalf("expected fresh persisted entry, got dup=%v state=%q", dup1, entry1.DeliveryState)
	}

	// Mark as enqueued (in-flight before crash) and verify duplicate event does not create a second entry.
	if err := st.UpdateDeliveryState(snap.RunID, entry1.DedupeKey, protocol.DeliveryEnqueued); err != nil {
		t.Fatalf("UpdateDeliveryState error = %v", err)
	}
	evDup := ev
	evDup.EventID = "evt-2-same-attempt"
	entry2, dup2, err := st.RecordBeforeSend(evDup)
	if err != nil {
		t.Fatalf("RecordBeforeSend(dup) error = %v", err)
	}
	if !dup2 || entry2.DeliveryState != protocol.DeliveryEnqueued {
		t.Fatalf("expected duplicate detection preserving state, got dup=%v state=%q", dup2, entry2.DeliveryState)
	}

	// Simulate truncated crash line in journal.jsonl and running state before crash.
	if err := st.UpdateRunState(snap.RunID, protocol.RunRunning, ""); err != nil {
		t.Fatalf("UpdateRunState error = %v", err)
	}
	journalPath := filepath.Join(baseDir, "runs", snap.RunID, "journal.jsonl")
	f, err := os.OpenFile(journalPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("OpenFile journal error = %v", err)
	}
	_, _ = f.WriteString(`{"event_id":"truncated-json-line"`)
	_ = f.Close()

	recovered, err := st.RecoverRun(snap.RunID)
	if err != nil {
		t.Fatalf("RecoverRun error = %v", err)
	}
	if recovered.TruncatedLines != 1 {
		t.Fatalf("expected 1 truncated line recorded, got %d", recovered.TruncatedLines)
	}
	if recovered.State != protocol.RunInterrupted {
		t.Fatalf("expected RunInterrupted after crash in RunRunning, got %q", recovered.State)
	}
	recEntry := recovered.Outbox[entry1.DedupeKey]
	if recEntry == nil || recEntry.DeliveryState != protocol.DeliveryUnknown {
		t.Fatalf("expected ambiguous in-flight delivery marked DeliveryUnknown, got %+v", recEntry)
	}
}
