package protocol

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type Role string

const (
	RoleMaestra Role = "maestra"
	RoleWorker  Role = "worker"
)

type Provider string

const (
	ProviderCodex       Provider = "codex"
	ProviderAntigravity Provider = "antigravity"
	ProviderClaude      Provider = "claude"
	ProviderCopilot     Provider = "copilot"

	DefaultAntigravityModel  = "gemini-3.8-flash-high"
	DefaultAntigravityEffort = "high"
	DefaultCodexModel        = "gpt-6.1-sol"
	DefaultCodexEffort       = "low"
)

func ResolveModelAndEffort(provider Provider, requestedModel, effort string) (string, string) {
	m := strings.TrimSpace(requestedModel)
	eff := strings.TrimSpace(effort)

	lower := strings.ToLower(m)
	// Parse optional "(low|medium|high|xhigh|max)" suffix if passed in model string, e.g. "sol 6.1(low)" or "gemini 3.8(high)"
	if idx := strings.Index(lower, "("); idx > 0 && strings.HasSuffix(lower, ")") {
		parsedEffort := strings.TrimSpace(lower[idx+1 : len(lower)-1])
		if eff == "" && parsedEffort != "" {
			eff = parsedEffort
		}
		m = strings.TrimSpace(m[:idx])
		lower = strings.ToLower(m)
	}

	switch provider {
	case ProviderAntigravity:
		switch lower {
		case "", "default", "gemini 3.8", "gemini-3.8", "gemini 3.8 flash", "gemini-3.8-flash":
			if eff == "" {
				eff = DefaultAntigravityEffort
			}
			m = fmt.Sprintf("gemini-3.8-flash-%s", eff)
		}
		if eff == "" {
			eff = DefaultAntigravityEffort
		}
	case ProviderCodex:
		switch lower {
		case "", "default", "sol 6.1", "sol-6.1", "gpt-6.1-sol":
			m = DefaultCodexModel
			if eff == "" {
				eff = DefaultCodexEffort
			}
		}
		if eff == "" {
			eff = DefaultCodexEffort
		}
	default:
		if m == "" {
			m = "default"
		}
	}
	return m, eff
}

func ValidProvider(p Provider) bool {
	switch p {
	case ProviderCodex, ProviderAntigravity, ProviderClaude, ProviderCopilot:
		return true
	default:
		return false
	}
}

type MaestraAction string

const (
	ActionDelegate MaestraAction = "delegate"
	ActionWait     MaestraAction = "wait"
	ActionFinish   MaestraAction = "finish"
	ActionAskUser  MaestraAction = "ask_user"
)

func ValidAction(a MaestraAction) bool {
	switch a {
	case ActionDelegate, ActionWait, ActionFinish, ActionAskUser:
		return true
	default:
		return false
	}
}

type RunState string

const (
	RunStarting     RunState = "starting"
	RunRunning      RunState = "running"
	RunWaiting      RunState = "waiting"
	RunAwaitingUser RunState = "awaiting_user"
	RunCompleting   RunState = "completing"
	RunCompleted    RunState = "completed"
	RunFailed       RunState = "failed"
	RunInterrupted  RunState = "interrupted"
)

type DeliveryState string

const (
	DeliveryPersisted       DeliveryState = "persisted"
	DeliveryEnqueued        DeliveryState = "enqueued"
	DeliveryStdoutDelivered DeliveryState = "stdout_delivered"
	DeliveryTurnStarted     DeliveryState = "turn_started"
	DeliveryProcessed       DeliveryState = "processed"
	DeliveryAcknowledged    DeliveryState = "acknowledged"
	DeliveryUnknown         DeliveryState = "unknown"
)

type EventType string

const (
	EventTaskDelegated         EventType = "task_delegated"
	EventWorkerCompleted       EventType = "worker_completed"
	EventWorkerFailed          EventType = "worker_failed"
	EventWorkerBlockedQuestion EventType = "worker_question"
	EventRunCompleted          EventType = "run_completed"
)

func ValidEventType(t EventType) bool {
	switch t {
	case EventTaskDelegated, EventWorkerCompleted, EventWorkerFailed, EventWorkerBlockedQuestion, EventRunCompleted:
		return true
	default:
		return false
	}
}

type EventStatus string

const (
	StatusRunning   EventStatus = "running"
	StatusSucceeded EventStatus = "succeeded"
	StatusFailed    EventStatus = "failed"
	StatusBlocked   EventStatus = "blocked"
)

func ValidEventStatus(s EventStatus) bool {
	switch s {
	case StatusRunning, StatusSucceeded, StatusFailed, StatusBlocked:
		return true
	default:
		return false
	}
}

type Usage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ThinkingTokens  int `json:"thinking_tokens"`
	CacheReadTokens int `json:"cache_read_tokens"`
	TotalTokens     int `json:"total_tokens"`
}

type Profile struct {
	Provider       Provider `json:"provider"`
	Role           Role     `json:"role"`
	CLIPath        string   `json:"cli_path,omitempty"`
	RequestedModel string   `json:"requested_model"`
	ObservedModel  string   `json:"observed_model"`
	Effort         string   `json:"effort,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	MaxTurns       int      `json:"max_turns"`
	ReadOnly       bool     `json:"read_only"`
}

func (p *Profile) NormalizeModels() {
	if p.Provider != "" {
		p.RequestedModel, p.Effort = ResolveModelAndEffort(p.Provider, p.RequestedModel, p.Effort)
	} else if strings.TrimSpace(p.RequestedModel) == "" {
		p.RequestedModel = "default"
	}
	if strings.TrimSpace(p.ObservedModel) == "" {
		p.ObservedModel = "unknown"
	}
}

func (p Profile) Validate() error {
	if !ValidProvider(p.Provider) {
		return fmt.Errorf("invalid provider %q", p.Provider)
	}
	if p.Role != RoleMaestra && p.Role != RoleWorker {
		return fmt.Errorf("invalid role %q", p.Role)
	}
	if p.TimeoutSeconds < 0 {
		return errors.New("timeout_seconds cannot be negative")
	}
	if p.MaxTurns < 0 {
		return errors.New("max_turns cannot be negative")
	}
	return nil
}

type TaskSpec struct {
	Target      string `json:"target"`
	Change      string `json:"change"`
	Constraints string `json:"constraints"`
	Ownership   string `json:"ownership"`
	Acceptance  string `json:"acceptance"`
	Prompt      string `json:"prompt"`
}

func (s TaskSpec) Validate() error {
	if strings.TrimSpace(s.Target) == "" {
		return errors.New("task spec target is required")
	}
	if strings.TrimSpace(s.Acceptance) == "" {
		return errors.New("task spec acceptance is required")
	}
	if strings.TrimSpace(s.Prompt) == "" && strings.TrimSpace(s.Change) == "" {
		return errors.New("task spec prompt or change is required")
	}
	return nil
}

type CommandEnvelope struct {
	Action           MaestraAction `json:"action"`
	RunKey           string        `json:"run_key,omitempty"`
	RunID            string        `json:"run_id"`
	TaskID           string        `json:"task_id,omitempty"`
	AttemptID        string        `json:"attempt_id,omitempty"`
	MaestraProvider  Provider      `json:"maestra_provider"`
	MaestraSessionID string        `json:"maestra_session_id"`
	WorkerProvider   Provider      `json:"worker_provider,omitempty"`
	WorkerSessionID  string        `json:"worker_session_id,omitempty"`
	WorkerProject    string        `json:"worker_project,omitempty"`
	WorkerChat       string        `json:"worker_chat,omitempty"`
	WorkerChatTitle  string        `json:"worker_chat_title,omitempty"`
	Workspace        string        `json:"workspace"`
	Profile          Profile       `json:"profile,omitempty"`
	Spec             TaskSpec      `json:"spec,omitempty"`
	Question         string        `json:"question,omitempty"`
}

func (c CommandEnvelope) EffectiveChatTitle() string {
	for _, candidate := range []string{c.WorkerChatTitle, c.WorkerChat, c.Spec.Change, c.Spec.Target} {
		s := strings.TrimSpace(candidate)
		if s != "" {
			if len(s) > 72 {
				return strings.TrimSpace(s[:72])
			}
			return s
		}
	}
	return "Delegated Task"
}

func (c *CommandEnvelope) Validate() error {
	if !ValidAction(c.Action) {
		return fmt.Errorf("unsupported maestra action %q", c.Action)
	}
	if strings.TrimSpace(c.RunID) == "" && strings.TrimSpace(c.RunKey) == "" {
		return errors.New("run_id or run_key is required")
	}
	if !ValidProvider(c.MaestraProvider) {
		return fmt.Errorf("invalid maestra_provider %q", c.MaestraProvider)
	}
	if strings.TrimSpace(c.MaestraSessionID) == "" || isAmbiguousSession(c.MaestraSessionID) {
		return fmt.Errorf("explicit maestra_session_id is required, got %q", c.MaestraSessionID)
	}
	if strings.TrimSpace(c.Workspace) == "" {
		return errors.New("workspace is required")
	}
	switch c.Action {
	case ActionDelegate:
		if strings.TrimSpace(c.TaskID) == "" {
			return errors.New("task_id is required for delegate")
		}
		if strings.TrimSpace(c.AttemptID) == "" {
			return errors.New("attempt_id is required for delegate")
		}
		if !ValidProvider(c.WorkerProvider) {
			return fmt.Errorf("invalid worker_provider %q", c.WorkerProvider)
		}
		if err := c.Spec.Validate(); err != nil {
			return err
		}
		if c.Profile.Provider == "" {
			c.Profile.Provider = c.WorkerProvider
		}
		c.Profile.NormalizeModels()
	case ActionAskUser:
		if strings.TrimSpace(c.Question) == "" {
			return errors.New("question is required for ask_user")
		}
	}
	return nil
}

type Event struct {
	EventID              string      `json:"event_id"`
	RunID                string      `json:"run_id"`
	TaskID               string      `json:"task_id"`
	AttemptID            string      `json:"attempt_id"`
	SourceProvider       Provider    `json:"source_provider"`
	SourceSessionID      string      `json:"source_session_id"`
	DestinationProvider  Provider    `json:"destination_provider"`
	DestinationSessionID string      `json:"destination_session_id"`
	Type                 EventType   `json:"type"`
	Status               EventStatus `json:"status"`
	Summary              string      `json:"summary"`
	Question             string      `json:"question,omitempty"`
	RequestedModel       string      `json:"requested_model"`
	ObservedModel        string      `json:"observed_model"`
	ArtifactRefs         []string    `json:"artifact_refs,omitempty"`
	OccurredAt           time.Time   `json:"occurred_at"`
	Usage                *Usage      `json:"usage,omitempty"`
}

func (e *Event) NormalizeModels() {
	if strings.TrimSpace(e.RequestedModel) == "" {
		e.RequestedModel = "default"
	}
	if strings.TrimSpace(e.ObservedModel) == "" {
		e.ObservedModel = "unknown"
	}
}

func (e *Event) DedupeKey() string {
	return fmt.Sprintf("%s:%s:%s:%s", e.RunID, e.TaskID, e.AttemptID, e.Type)
}

func (e *Event) Validate() error {
	e.NormalizeModels()
	if strings.TrimSpace(e.EventID) == "" {
		return errors.New("event_id is required")
	}
	if strings.TrimSpace(e.RunID) == "" {
		return errors.New("run_id is required")
	}
	if strings.TrimSpace(e.TaskID) == "" {
		return errors.New("task_id is required")
	}
	if strings.TrimSpace(e.AttemptID) == "" {
		return errors.New("attempt_id is required")
	}
	if !ValidProvider(e.SourceProvider) {
		return fmt.Errorf("invalid source_provider %q", e.SourceProvider)
	}
	if strings.TrimSpace(e.SourceSessionID) == "" || isAmbiguousSession(e.SourceSessionID) {
		return fmt.Errorf("explicit source_session_id is required, got %q", e.SourceSessionID)
	}
	if !ValidProvider(e.DestinationProvider) {
		return fmt.Errorf("invalid destination_provider %q", e.DestinationProvider)
	}
	if strings.TrimSpace(e.DestinationSessionID) == "" || isAmbiguousSession(e.DestinationSessionID) {
		return fmt.Errorf("explicit destination_session_id is required, got %q", e.DestinationSessionID)
	}
	if !ValidEventType(e.Type) {
		return fmt.Errorf("invalid event type %q", e.Type)
	}
	if !ValidEventStatus(e.Status) {
		return fmt.Errorf("invalid event status %q", e.Status)
	}
	if strings.TrimSpace(e.Summary) == "" {
		return errors.New("summary is required")
	}
	if e.Type == EventWorkerBlockedQuestion && strings.TrimSpace(e.Question) == "" {
		return errors.New("question is required when event type is worker_question")
	}
	if e.OccurredAt.IsZero() {
		return errors.New("occurred_at is required")
	}
	return nil
}

func isAmbiguousSession(id string) bool {
	lower := strings.ToLower(strings.TrimSpace(id))
	switch lower {
	case "latest", "last", "recent", "most-recent", "ultima", "última", "default":
		return true
	default:
		return false
	}
}
