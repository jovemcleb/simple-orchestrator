package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
	"github.com/jovemcleb/simple-orchestrator/internal/runtime"
)

type fakeCLIWorker struct {
	summary string
}

func (f *fakeCLIWorker) RunTask(ctx context.Context, cmd protocol.CommandEnvelope) (runtime.WorkerResult, error) {
	return runtime.WorkerResult{
		WorkerSessionID: "worker-sess-123",
		ObservedModel:   "Gemini 3.8 Flash (High)",
		Status:          protocol.StatusSucceeded,
		Summary:         f.summary,
		Usage:           &protocol.Usage{InputTokens: 50, OutputTokens: 5, TotalTokens: 55},
	}, nil
}

func TestRunCLI_EndToEndWithSpacesAndIdempotentKey(t *testing.T) {
	t.Parallel()
	baseWithSpaces := filepath.Join(t.TempDir(), "Example User", "Orchestrator State")
	workspaceWithSpaces := filepath.Join(t.TempDir(), "OneDrive Docs", "sample project")

	workers := map[protocol.Provider]runtime.WorkerRunner{
		protocol.ProviderAntigravity: &fakeCLIWorker{summary: "MYO_BRIDGE_OK"},
	}

	delegateArgs := []string{
		"delegate",
		"--state-dir", baseWithSpaces,
		"--run-key", "key-first-pair",
		"--task-id", "task-1",
		"--attempt-id", "att-1",
		"--maestra-provider", "codex",
		"--maestra-session", "01a15543-4df3-78b3-853e-3dd07a139b28",
		"--worker-provider", "antigravity",
		"--workspace", workspaceWithSpaces,
		"--target", "docs/spikes/native-return.md",
		"--acceptance", "Exact token MYO_BRIDGE_OK",
		"--prompt", "Respond MYO_BRIDGE_OK",
	}

	var out1, err1 bytes.Buffer
	code1 := runCLI(context.Background(), delegateArgs, &out1, &err1, workers)
	if code1 != 0 {
		t.Fatalf("first delegate exit=%d stderr=%s", code1, err1.String())
	}

	var resp1 struct {
		OK        bool           `json:"ok"`
		Duplicate bool           `json:"duplicate"`
		Event     protocol.Event `json:"event"`
	}
	if err := json.Unmarshal(out1.Bytes(), &resp1); err != nil {
		t.Fatalf("unmarshal resp1 error = %v", err)
	}
	if !resp1.OK || resp1.Duplicate || resp1.Event.Summary != "MYO_BRIDGE_OK" {
		t.Fatalf("unexpected resp1: %+v", resp1)
	}

	// Second call with same run-key and task/attempt returns same run_id and duplicate=true.
	var out2, err2 bytes.Buffer
	code2 := runCLI(context.Background(), delegateArgs, &out2, &err2, workers)
	if code2 != 0 {
		t.Fatalf("second delegate exit=%d stderr=%s", code2, err2.String())
	}
	var resp2 struct {
		OK        bool           `json:"ok"`
		Duplicate bool           `json:"duplicate"`
		Event     protocol.Event `json:"event"`
	}
	if err := json.Unmarshal(out2.Bytes(), &resp2); err != nil {
		t.Fatalf("unmarshal resp2 error = %v", err)
	}
	if !resp2.Duplicate || resp2.Event.RunID != resp1.Event.RunID {
		t.Fatalf("expected duplicate reuse of same RunID, got %+v", resp2)
	}

	// Ack and Finish.
	var ackOut, ackErr bytes.Buffer
	if code := runCLI(context.Background(), []string{
		"ack",
		"--state-dir", baseWithSpaces,
		"--run-id", resp1.Event.RunID,
		"--task-id", "task-1",
		"--attempt-id", "att-1",
	}, &ackOut, &ackErr, nil); code != 0 {
		t.Fatalf("ack failed: %s", ackErr.String())
	}

	var finOut, finErr bytes.Buffer
	if code := runCLI(context.Background(), []string{
		"finish",
		"--state-dir", baseWithSpaces,
		"--run-id", resp1.Event.RunID,
	}, &finOut, &finErr, nil); code != 0 {
		t.Fatalf("finish failed: %s", finErr.String())
	}
}

func TestRunCLI_RejectsInvalidSession(t *testing.T) {
	t.Parallel()
	var out, errBuf bytes.Buffer
	code := runCLI(context.Background(), []string{
		"delegate",
		"--state-dir", t.TempDir(),
		"--run-key", "bad-key",
		"--maestra-provider", "codex",
		"--maestra-session", "latest",
		"--worker-provider", "antigravity",
		"--workspace", t.TempDir(),
		"--target", "README.md",
		"--acceptance", "ok",
		"--prompt", "ok",
	}, &out, &errBuf, nil)
	if code == 0 {
		t.Fatalf("expected non-zero exit code for ambiguous session 'latest'")
	}
}
