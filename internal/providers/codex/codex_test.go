package codex

import (
	"errors"
	"testing"

	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
)

func TestParseStreamJSONL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		stdout      string
		stderr      string
		exitErr     error
		wantStatus  protocol.EventStatus
		wantSession string
		wantSummary string
		wantTokens  int
		wantErr     bool
	}{
		{
			name: "valid turn completed with usage",
			stdout: `{"type":"thread.started","thread_id":"01a15543-4df3-78b3-853e-3dd07a139b28"}
{"type":"item.completed","item":{"type":"agent_message","text":"MYO_CODEX_OK"}}
{"type":"turn.completed","usage":{"input_tokens":120,"cached_input_tokens":40,"output_tokens":15,"output_tokens_details":{"reasoning_tokens":5}}}
`,
			wantStatus:  protocol.StatusSucceeded,
			wantSession: "01a15543-4df3-78b3-853e-3dd07a139b28",
			wantSummary: "MYO_CODEX_OK",
			wantTokens:  135,
			wantErr:     false,
		},
		{
			name: "text emitted but terminal event is turn.failed",
			stdout: `{"type":"thread.started","thread_id":"01a15543-4df3-78b3-853e-3dd07a139b28"}
{"type":"item.completed","item":{"type":"agent_message","text":"Partial answer"}}
{"type":"turn.failed","error":{"message":"quota exceeded for requested model"}}
`,
			wantStatus:  protocol.StatusFailed,
			wantSession: "01a15543-4df3-78b3-853e-3dd07a139b28",
			wantSummary: "quota exceeded for requested model",
			wantErr:     true,
		},
		{
			name: "incomplete EOF without turn.completed fails even with exit 0",
			stdout: `{"type":"thread.started","thread_id":"01a15543-4df3-78b3-853e-3dd07a139b28"}
{"type":"item.completed","item":{"type":"agent_message","text":"Half stream"}}
`,
			wantStatus:  protocol.StatusFailed,
			wantSession: "01a15543-4df3-78b3-853e-3dd07a139b28",
			wantErr:     true,
		},
		{
			name:       "non-zero exit error fails",
			stdout:     "",
			stderr:     "authentication required",
			exitErr:    errors.New("exit status 1"),
			wantStatus: protocol.StatusFailed,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := ParseStreamJSONL([]byte(tt.stdout), []byte(tt.stderr), tt.exitErr, "fallback-thread")
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
