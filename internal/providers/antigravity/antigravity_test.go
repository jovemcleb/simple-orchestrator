package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
)

func TestIsolateGlobalMCPConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	original := `{"mcpServers":{"open-design":{"command":"Open Design.exe","disabled":false}}}` + "\n"
	if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	shaBefore, restore, err := IsolateGlobalMCPConfig(context.Background(), cfgPath)
	if err != nil {
		t.Fatalf("IsolateGlobalMCPConfig error = %v", err)
	}
	if shaBefore == "" {
		t.Fatalf("expected non-empty shaBefore")
	}

	// Verify lock file exists during isolation.
	if _, err := os.Stat(cfgPath + ".myo.lock"); err != nil {
		t.Fatalf("expected inter-process lock file during isolation: %v", err)
	}

	duringBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile during isolation error = %v", err)
	}
	if string(duringBytes) == original {
		t.Fatalf("expected mcp_config.json to have empty mcpServers during isolation")
	}

	shaAfter, err := restore()
	if err != nil {
		t.Fatalf("restore() error = %v", err)
	}
	if shaAfter != shaBefore {
		t.Fatalf("SHA-256 mismatch: before=%s after=%s", shaBefore, shaAfter)
	}
	if _, err := os.Stat(cfgPath + ".myo.lock"); !os.IsNotExist(err) {
		t.Fatalf("expected lock file removed after restore")
	}
}

func TestBuildArgs_DefaultZeroPrintTimeout(t *testing.T) {
	t.Parallel()
	adapter := NewAdapter("agy.EXE", "", false)
	args := adapter.BuildArgs(protocol.CommandEnvelope{
		WorkerProvider: protocol.ProviderAntigravity,
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--print-timeout 0s") {
		t.Fatalf("expected --print-timeout 0s by default (event-driven, no timeout), got: %s", joined)
	}
	if !strings.Contains(joined, "--mode auto") || !strings.Contains(joined, "--dangerously-skip-permissions") {
		t.Fatalf("expected --mode auto and --dangerously-skip-permissions, got: %s", joined)
	}
}

func TestAdapter_RunTask_PropagatesRestoreError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	original := `{"mcpServers":{"open-design":{"command":"Open Design.exe"}}}` + "\n"
	if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	// Fake CLI using cmd.exe /c echo to output a valid stream-json result, while AfterIsolateHook replaces cfgPath with a directory so restore() fails.
	adapter := NewAdapter("cmd.exe", cfgPath, true)
	adapter.AfterIsolateHook = func(p string) {
		_ = os.Remove(p)
		_ = os.MkdirAll(filepath.Join(p, "blocking-subdir"), 0o755)
	}

	cmd := protocol.CommandEnvelope{
		Action:           protocol.ActionDelegate,
		RunID:            "run-restore-err",
		TaskID:           "task-1",
		AttemptID:        "att-1",
		MaestraProvider:  protocol.ProviderCodex,
		MaestraSessionID: "sess-1",
		WorkerProvider:   protocol.ProviderAntigravity,
		Workspace:        dir,
		Spec: protocol.TaskSpec{
			Target:     "README.md",
			Acceptance: "ok",
			Prompt:     "ok",
		},
	}

	res, err := adapter.RunTask(context.Background(), cmd)
	if err == nil || !strings.Contains(err.Error(), "failed to restore mcp_config.json") {
		t.Fatalf("expected restore error propagated from RunTask, got res=%+v err=%v", res, err)
	}
	if res.Status != protocol.StatusFailed {
		t.Fatalf("expected StatusFailed when restore fails, got %q", res.Status)
	}
}

func TestParseStreamJSONL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		stdout      string
		stderr      string
		wantStatus  protocol.EventStatus
		wantSession string
		wantSummary string
		wantTokens  int
		wantErr     bool
	}{
		{
			name: "valid T01 stream-json result with usage",
			stdout: `{"event":"init","conversation_id":"b2ffec5f-bc52-4990-8657-031faa8ba2a9"}
{"event":"step_update"}
{"event":"result","result":{"conversation_id":"b2ffec5f-bc52-4990-8657-031faa8ba2a9","status":"SUCCESS","response":"MYO_T01_WORKER_OK\n","num_turns":1,"usage":{"input_tokens":9084,"output_tokens":295,"thinking_tokens":284,"cache_read_tokens":0,"total_tokens":9379}}}
`,
			stderr:      "",
			wantStatus:  protocol.StatusSucceeded,
			wantSession: "b2ffec5f-bc52-4990-8657-031faa8ba2a9",
			wantSummary: "MYO_T01_WORKER_OK",
			wantTokens:  9379,
			wantErr:     false,
		},
		{
			name: "SUCCESS with print timeout on stderr and empty response must fail (T01 lesson)",
			stdout: `{"event":"init","conversation_id":"17c851de-1c08-4514-820a-e8387b9f1f2d"}
{"event":"result","result":{"conversation_id":"17c851de-1c08-4514-820a-e8387b9f1f2d","status":"SUCCESS","response":"","num_turns":0}}
`,
			stderr:      "[agy] print timeout after 1m0s with turn in progress; returning partial output",
			wantStatus:  protocol.StatusFailed,
			wantSession: "17c851de-1c08-4514-820a-e8387b9f1f2d",
			wantErr:     true,
		},
		{
			name:        "EOF without result event fails",
			stdout:      `{"event":"init","conversation_id":"17c851de-1c08-4514-820a-e8387b9f1f2d"}`,
			stderr:      "",
			wantStatus:  protocol.StatusFailed,
			wantSession: "17c851de-1c08-4514-820a-e8387b9f1f2d",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := ParseStreamJSONL([]byte(tt.stdout), []byte(tt.stderr), nil, "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseStreamJSONL() error = %v, wantErr %v", err, tt.wantErr)
			}
			if res.Status != tt.wantStatus {
				t.Fatalf("Status = %q, want %q", res.Status, tt.wantStatus)
			}
			if tt.wantSession != "" && res.WorkerSessionID != tt.wantSession {
				t.Fatalf("WorkerSessionID = %q, want %q", res.WorkerSessionID, tt.wantSession)
			}
			if tt.wantSummary != "" && res.Summary != tt.wantSummary {
				t.Fatalf("Summary = %q, want %q", res.Summary, tt.wantSummary)
			}
			if tt.wantTokens > 0 && (res.Usage == nil || res.Usage.TotalTokens != tt.wantTokens) {
				t.Fatalf("TotalTokens = %+v, want %d", res.Usage, tt.wantTokens)
			}
		})
	}
}
