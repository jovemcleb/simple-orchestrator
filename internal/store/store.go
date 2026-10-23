package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
)

type OutboxEntry struct {
	DedupeKey     string                 `json:"dedupe_key"`
	Event         protocol.Event         `json:"event"`
	DeliveryState protocol.DeliveryState `json:"delivery_state"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

type RunSnapshot struct {
	RunID            string                  `json:"run_id"`
	RunKey           string                  `json:"run_key,omitempty"`
	State            protocol.RunState       `json:"state"`
	MaestraProvider  protocol.Provider       `json:"maestra_provider"`
	MaestraSessionID string                  `json:"maestra_session_id"`
	Workspace        string                  `json:"workspace"`
	PendingQuestion  string                  `json:"pending_question,omitempty"`
	TruncatedLines   int                     `json:"truncated_lines,omitempty"`
	Outbox           map[string]*OutboxEntry `json:"outbox"`
	UpdatedAt        time.Time               `json:"updated_at"`
}

type Store struct {
	mu      sync.Mutex
	baseDir string
}

func DefaultBaseDir() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if strings.TrimSpace(localAppData) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		localAppData = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(localAppData, "SimpleOrchestrator"), nil
}

func New(baseDir string) (*Store, error) {
	if strings.TrimSpace(baseDir) == "" {
		var err error
		baseDir, err = DefaultBaseDir()
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Join(baseDir, "runs"), 0o755); err != nil {
		return nil, err
	}
	return &Store{baseDir: baseDir}, nil
}

func (s *Store) BaseDir() string { return s.baseDir }

func (s *Store) runDir(runID string) string {
	return filepath.Join(s.baseDir, "runs", runID)
}

func (s *Store) keysPath() string {
	return filepath.Join(s.baseDir, "run_keys.json")
}

func (s *Store) CreateOrGetRun(runKey, requestedRunID string, maestraProvider protocol.Provider, maestraSessionID, workspace string) (*RunSnapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !protocol.ValidProvider(maestraProvider) {
		return nil, false, fmt.Errorf("invalid maestra provider %q", maestraProvider)
	}
	if strings.TrimSpace(maestraSessionID) == "" {
		return nil, false, errors.New("maestra_session_id is required")
	}
	if strings.TrimSpace(workspace) == "" {
		return nil, false, errors.New("workspace is required")
	}

	keys := map[string]string{}
	if data, err := os.ReadFile(s.keysPath()); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &keys)
	}

	runID := strings.TrimSpace(requestedRunID)
	if strings.TrimSpace(runKey) != "" {
		if existingID, ok := keys[runKey]; ok && existingID != "" {
			snap, err := s.loadSnapshotUnlocked(existingID)
			if err == nil {
				return snap, true, nil
			}
		}
	}
	if runID != "" {
		if snap, err := s.loadSnapshotUnlocked(runID); err == nil {
			return snap, true, nil
		}
	}
	if runID == "" {
		runID = fmt.Sprintf("run-%d", time.Now().UTC().UnixNano())
	}

	if err := os.MkdirAll(s.runDir(runID), 0o755); err != nil {
		return nil, false, err
	}

	snap := &RunSnapshot{
		RunID:            runID,
		RunKey:           runKey,
		State:            protocol.RunStarting,
		MaestraProvider:  maestraProvider,
		MaestraSessionID: maestraSessionID,
		Workspace:        workspace,
		Outbox:           make(map[string]*OutboxEntry),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := s.saveSnapshotUnlocked(snap); err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(runKey) != "" {
		keys[runKey] = runID
		if err := writeJSONAtomic(s.keysPath(), keys); err != nil {
			return nil, false, err
		}
	}
	return snap, false, nil
}

func (s *Store) RecordBeforeSend(ev protocol.Event) (*OutboxEntry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ev.Validate(); err != nil {
		return nil, false, err
	}
	snap, err := s.loadSnapshotUnlocked(ev.RunID)
	if err != nil {
		return nil, false, err
	}
	if snap.Outbox == nil {
		snap.Outbox = make(map[string]*OutboxEntry)
	}

	key := ev.DedupeKey()
	if existing, ok := snap.Outbox[key]; ok {
		return existing, true, nil
	}
	for _, existing := range snap.Outbox {
		if existing.Event.EventID == ev.EventID {
			return existing, true, nil
		}
	}

	if err := s.appendJournalLineUnlocked(ev); err != nil {
		return nil, false, err
	}

	entry := &OutboxEntry{
		DedupeKey:     key,
		Event:         ev,
		DeliveryState: protocol.DeliveryPersisted,
		UpdatedAt:     time.Now().UTC(),
	}
	snap.Outbox[key] = entry
	if ev.Type == protocol.EventWorkerBlockedQuestion {
		snap.State = protocol.RunAwaitingUser
		snap.PendingQuestion = ev.Question
	}
	snap.UpdatedAt = time.Now().UTC()
	if err := s.saveSnapshotUnlocked(snap); err != nil {
		return nil, false, err
	}
	return entry, false, nil
}

func (s *Store) UpdateDeliveryState(runID, dedupeKey string, newState protocol.DeliveryState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.loadSnapshotUnlocked(runID)
	if err != nil {
		return err
	}
	entry, ok := snap.Outbox[dedupeKey]
	if !ok {
		return fmt.Errorf("outbox entry %q not found in run %q", dedupeKey, runID)
	}
	if entry.DeliveryState == protocol.DeliveryAcknowledged && newState != protocol.DeliveryAcknowledged {
		return nil
	}
	entry.DeliveryState = newState
	entry.UpdatedAt = time.Now().UTC()
	snap.UpdatedAt = entry.UpdatedAt
	return s.saveSnapshotUnlocked(snap)
}

func (s *Store) UpdateRunState(runID string, state protocol.RunState, pendingQuestion string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.loadSnapshotUnlocked(runID)
	if err != nil {
		return err
	}
	snap.State = state
	snap.PendingQuestion = pendingQuestion
	snap.UpdatedAt = time.Now().UTC()
	return s.saveSnapshotUnlocked(snap)
}

func (s *Store) LoadSnapshot(runID string) (*RunSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadSnapshotUnlocked(runID)
}

func (s *Store) RecoverRun(runID string) (*RunSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.loadSnapshotUnlocked(runID)
	if err != nil {
		return nil, err
	}
	if snap.Outbox == nil {
		snap.Outbox = make(map[string]*OutboxEntry)
	}

	journalPath := filepath.Join(s.runDir(runID), "journal.jsonl")
	data, err := os.ReadFile(journalPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	truncated := 0
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev protocol.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			truncated++
			continue
		}
		if err := ev.Validate(); err != nil {
			truncated++
			continue
		}
		key := ev.DedupeKey()
		if _, exists := snap.Outbox[key]; !exists {
			snap.Outbox[key] = &OutboxEntry{
				DedupeKey:     key,
				Event:         ev,
				DeliveryState: protocol.DeliveryPersisted,
				UpdatedAt:     time.Now().UTC(),
			}
		}
	}

	for _, entry := range snap.Outbox {
		if entry.DeliveryState == protocol.DeliveryEnqueued || entry.DeliveryState == protocol.DeliveryTurnStarted {
			entry.DeliveryState = protocol.DeliveryUnknown
			entry.UpdatedAt = time.Now().UTC()
		}
	}
	snap.TruncatedLines = truncated
	if snap.State == protocol.RunRunning {
		snap.State = protocol.RunInterrupted
	}
	snap.UpdatedAt = time.Now().UTC()
	if err := s.saveSnapshotUnlocked(snap); err != nil {
		return nil, err
	}
	return snap, nil
}

func (s *Store) appendJournalLineUnlocked(ev protocol.Event) error {
	journalPath := filepath.Join(s.runDir(ev.RunID), "journal.jsonl")
	f, err := os.OpenFile(journalPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	encoded, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func (s *Store) loadSnapshotUnlocked(runID string) (*RunSnapshot, error) {
	path := filepath.Join(s.runDir(runID), "snapshot.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap RunSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	if snap.Outbox == nil {
		snap.Outbox = make(map[string]*OutboxEntry)
	}
	return &snap, nil
}

func (s *Store) saveSnapshotUnlocked(snap *RunSnapshot) error {
	path := filepath.Join(s.runDir(snap.RunID), "snapshot.json")
	return writeJSONAtomic(path, snap)
}

func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
