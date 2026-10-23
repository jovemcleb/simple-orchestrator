package protocol

import (
	"testing"
	"time"
)

func TestValidProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		provider Provider
		want     bool
	}{
		{name: "codex valid", provider: ProviderCodex, want: true},
		{name: "antigravity valid", provider: ProviderAntigravity, want: true},
		{name: "claude valid", provider: ProviderClaude, want: true},
		{name: "copilot valid", provider: ProviderCopilot, want: true},
		{name: "unknown provider invalid", provider: Provider("orca"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidProvider(tt.provider); got != tt.want {
				t.Fatalf("ValidProvider(%q) = %v, want %v", tt.provider, got, tt.want)
			}
		})
	}
}

func TestResolveModelAndEffort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		provider   Provider
		model      string
		effort     string
		wantModel  string
		wantEffort string
	}{
		{
			name:       "antigravity default resolves to gemini-3.8-flash-high and high",
			provider:   ProviderAntigravity,
			model:      "default",
			effort:     "",
			wantModel:  "gemini-3.8-flash-high",
			wantEffort: "high",
		},
		{
			name:       "antigravity alias gemini 3.8(high)",
			provider:   ProviderAntigravity,
			model:      "gemini 3.8(high)",
			effort:     "",
			wantModel:  "gemini-3.8-flash-high",
			wantEffort: "high",
		},
		{
			name:       "codex default resolves to gpt-6.1-sol and low",
			provider:   ProviderCodex,
			model:      "default",
			effort:     "",
			wantModel:  "gpt-6.1-sol",
			wantEffort: "low",
		},
		{
			name:       "codex alias sol 6.1(low)",
			provider:   ProviderCodex,
			model:      "sol 6.1(low)",
			effort:     "",
			wantModel:  "gpt-6.1-sol",
			wantEffort: "low",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotModel, gotEffort := ResolveModelAndEffort(tt.provider, tt.model, tt.effort)
			if gotModel != tt.wantModel || gotEffort != tt.wantEffort {
				t.Fatalf("ResolveModelAndEffort(%q, %q, %q) = (%q, %q), want (%q, %q)",
					tt.provider, tt.model, tt.effort, gotModel, gotEffort, tt.wantModel, tt.wantEffort)
			}
		})
	}
}

func TestCommandEnvelope_Validate(t *testing.T) {
	t.Parallel()
	validDelegate := CommandEnvelope{
		Action:           ActionDelegate,
		RunID:            "run-001",
		TaskID:           "task-01",
		AttemptID:        "att-01",
		MaestraProvider:  ProviderCodex,
		MaestraSessionID: "01a15543-4df3-78b3-853e-3dd07a139b28",
		WorkerProvider:   ProviderAntigravity,
		Workspace:        `C:\Users\Example User\projects\sample app`,
		Spec: TaskSpec{
			Target:      "docs/spikes/native-return.md",
			Change:      "Inspect read-only evidence",
			Constraints: "Read-only; no product writes",
			Ownership:   "worker",
			Acceptance:  "Structured summary returned",
			Prompt:      "Respond with summary",
		},
	}

	tests := []struct {
		name    string
		mutate  func(c *CommandEnvelope)
		wantErr bool
	}{
		{
			name:    "valid delegate command",
			mutate:  func(c *CommandEnvelope) {},
			wantErr: false,
		},
		{
			name: "rejects unknown action",
			mutate: func(c *CommandEnvelope) {
				c.Action = MaestraAction("reconfigure_flow")
			},
			wantErr: true,
		},
		{
			name: "rejects ambiguous latest session id",
			mutate: func(c *CommandEnvelope) {
				c.MaestraSessionID = "latest"
			},
			wantErr: true,
		},
		{
			name: "rejects missing worker provider on delegate",
			mutate: func(c *CommandEnvelope) {
				c.WorkerProvider = ""
			},
			wantErr: true,
		},
		{
			name: "normalizes unknown model fields when empty",
			mutate: func(c *CommandEnvelope) {
				c.Profile.RequestedModel = ""
				c.Profile.ObservedModel = ""
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := validDelegate
			tt.mutate(&cmd)
			err := cmd.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && cmd.Action == ActionDelegate {
				if cmd.Profile.RequestedModel == "" || cmd.Profile.ObservedModel == "" {
					t.Fatalf("expected normalized model fields, got requested=%q observed=%q",
						cmd.Profile.RequestedModel, cmd.Profile.ObservedModel)
				}
			}
		})
	}
}

func TestEvent_Validate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 21, 17, 40, 0, 0, time.UTC)
	base := Event{
		EventID:              "evt-1",
		RunID:                "run-1",
		TaskID:               "task-1",
		AttemptID:            "att-1",
		SourceProvider:       ProviderAntigravity,
		SourceSessionID:      "b2ffec5f-bc52-4990-8657-031faa8ba2a9",
		DestinationProvider:  ProviderCodex,
		DestinationSessionID: "01a15543-4df3-78b3-853e-3dd07a139b28",
		Type:                 EventWorkerCompleted,
		Status:               StatusSucceeded,
		Summary:              "MYO_T01_WORKER_OK",
		OccurredAt:           now,
	}

	tests := []struct {
		name    string
		mutate  func(e *Event)
		wantErr bool
	}{
		{
			name:    "valid worker completed event with default/unknown models",
			mutate:  func(e *Event) {},
			wantErr: false,
		},
		{
			name: "missing source session id fails",
			mutate: func(e *Event) {
				e.SourceSessionID = ""
			},
			wantErr: true,
		},
		{
			name: "ambiguous destination session id fails",
			mutate: func(e *Event) {
				e.DestinationSessionID = "ultima"
			},
			wantErr: true,
		},
		{
			name: "worker question without question text fails",
			mutate: func(e *Event) {
				e.Type = EventWorkerBlockedQuestion
				e.Status = StatusBlocked
				e.Question = ""
			},
			wantErr: true,
		},
		{
			name: "malformed event type fails",
			mutate: func(e *Event) {
				e.Type = EventType("unknown_event")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ev := base
			tt.mutate(&ev)
			err := ev.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Event.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if ev.RequestedModel != "default" || ev.ObservedModel != "unknown" {
					t.Fatalf("expected default/unknown model normalization, got %q / %q",
						ev.RequestedModel, ev.ObservedModel)
				}
			}
		})
	}
}
