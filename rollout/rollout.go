package rollout

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"codex_go/session"

	"github.com/klauspost/compress/zstd"
)

const (
	SessionsSubdir         = "sessions"
	ArchivedSessionsSubdir = "archived_sessions"
)

var materializeReferenceMu sync.Mutex

type SortKey string

const (
	SortCreatedAt SortKey = "created_at"
	SortUpdatedAt SortKey = "updated_at"
	SortRecencyAt SortKey = "recency_at"
)

type SortDirection string

const (
	SortAsc  SortDirection = "asc"
	SortDesc SortDirection = "desc"
)

type Config struct {
	CodexHome        string
	SQLiteHome       string
	CWD              string
	Model            string
	ModelProviderID  string
	GenerateMemories bool
}

type Item struct {
	ID         string          `json:"id,omitempty"`
	Type       string          `json:"type"`
	Role       string          `json:"role,omitempty"`
	Text       string          `json:"text,omitempty"`
	Name       string          `json:"name,omitempty"`
	CallID     string          `json:"call_id,omitempty"`
	Content    []ContentPart   `json:"content,omitempty"`
	Data       map[string]any  `json:"data,omitempty"`
	Raw        json.RawMessage `json:"raw,omitempty"`
	ResponseID string          `json:"response_id,omitempty"`
	Metadata   map[string]any  `json:"metadata,omitempty"`
}

type ContentPart struct {
	Type     string  `json:"type"`
	Text     string  `json:"text,omitempty"`
	ImageURL string  `json:"image_url,omitempty"`
	Detail   *string `json:"detail,omitempty"`
}

type SessionMeta struct {
	ID                          string                              `json:"id"`
	SessionID                   string                              `json:"session_id"`
	SessionPrefix               string                              `json:"session_prefix,omitempty"`
	ForkedFromID                string                              `json:"forked_from_id,omitempty"`
	Timestamp                   string                              `json:"timestamp"`
	CWD                         string                              `json:"cwd"`
	Source                      string                              `json:"source,omitempty"`
	ThreadSource                string                              `json:"thread_source,omitempty"`
	Originator                  string                              `json:"originator"`
	Model                       string                              `json:"model,omitempty"`
	ModelProvider               string                              `json:"model_provider,omitempty"`
	HistoryMode                 string                              `json:"history_mode,omitempty"`
	HistoryBase                 *HistoryPosition                    `json:"history_base,omitempty"`
	SubagentHistoryStartOrdinal *uint64                             `json:"subagent_history_start_ordinal,omitempty"`
	MemoryMode                  string                              `json:"memory_mode,omitempty"`
	ParentThreadID              string                              `json:"parent_thread_id,omitempty"`
	BaseInstructions            string                              `json:"base_instructions,omitempty"`
	BaseInstructionsProvenance  *session.BaseInstructionsProvenance `json:"-"`
	AgentNickname               string                              `json:"agent_nickname,omitempty"`
	AgentRole                   string                              `json:"agent_role,omitempty"`
	AgentPath                   string                              `json:"agent_path,omitempty"`
	DynamicTools                []json.RawMessage                   `json:"dynamic_tools,omitempty"`
	SelectedCapabilityRoots     []json.RawMessage                   `json:"selected_capability_roots,omitempty"`
	MultiAgentVersion           string                              `json:"multi_agent_version,omitempty"`
	ContextWindow               json.RawMessage                     `json:"context_window,omitempty"`
	CLIVersion                  string                              `json:"cli_version"`
	Git                         map[string]string                   `json:"git,omitempty"`
	Extra                       map[string]any                      `json:"extra,omitempty"`
}

// UnmarshalJSON accepts both the legacy Go string representation and the
// object representation emitted by current Rust rollouts:
// {"base_instructions":"..."} and {"base_instructions":{"text":"..."}}.
func (m *SessionMeta) UnmarshalJSON(data []byte) error {
	type sessionMetaAlias SessionMeta
	var wire struct {
		*sessionMetaAlias
		BaseInstructions json.RawMessage `json:"base_instructions"`
	}
	wire.sessionMetaAlias = (*sessionMetaAlias)(m)
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if len(wire.BaseInstructions) == 0 || string(wire.BaseInstructions) == "null" {
		m.BaseInstructions = ""
		m.BaseInstructionsProvenance = nil
		return nil
	}
	var legacy string
	if err := json.Unmarshal(wire.BaseInstructions, &legacy); err == nil {
		m.BaseInstructions = legacy
		m.BaseInstructionsProvenance = nil
		return nil
	}
	var object struct {
		Text       string                              `json:"text"`
		Provenance *session.BaseInstructionsProvenance `json:"provenance,omitempty"`
	}
	if err := json.Unmarshal(wire.BaseInstructions, &object); err != nil {
		return fmt.Errorf("base_instructions: expected string or object: %w", err)
	}
	m.BaseInstructions = object.Text
	m.BaseInstructionsProvenance = object.Provenance
	return nil
}

// MarshalJSON emits the Rust-compatible object representation while retaining
// the Go string field internally for callers and legacy metadata.
func (m SessionMeta) MarshalJSON() ([]byte, error) {
	type sessionMetaAlias SessionMeta
	var wire struct {
		*sessionMetaAlias
		BaseInstructions any `json:"base_instructions,omitempty"`
	}
	wire.sessionMetaAlias = (*sessionMetaAlias)(&m)
	if m.BaseInstructions != "" {
		wire.BaseInstructions = struct {
			Text       string                              `json:"text"`
			Provenance *session.BaseInstructionsProvenance `json:"provenance,omitempty"`
		}{Text: m.BaseInstructions, Provenance: cloneBaseInstructionsProvenance(m.BaseInstructionsProvenance)}
	}
	return json.Marshal(&wire)
}

func cloneBaseInstructionsProvenance(value *session.BaseInstructionsProvenance) *session.BaseInstructionsProvenance {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

type HistoryPosition struct {
	ThreadID            string `json:"thread_id"`
	EndOrdinalExclusive uint64 `json:"end_ordinal_exclusive"`
	EndByteOffset       uint64 `json:"end_byte_offset"`
}

type Line struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp,omitempty"`
	Ordinal   *uint64         `json:"ordinal,omitempty"`
	Meta      *SessionMeta    `json:"meta,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Item      json.RawMessage `json:"item,omitempty"`
	// ItemMetadata is the optional harness-owned metadata sibling stored
	// beside a response-item payload (Rust RolloutItemWire::ResponseItem
	// metadata field, #38058). Absent for legacy lines.
	ItemMetadata     json.RawMessage `json:"metadata,omitempty"`
	ItemID           string          `json:"item_id,omitempty"`
	TurnID           string          `json:"turn_id,omitempty"`
	Role             string          `json:"role,omitempty"`
	ResponseID       string          `json:"response_id,omitempty"`
	ThreadRolledBack *RollbackEvent  `json:"thread_rolled_back,omitempty"`
	TurnContext      json.RawMessage `json:"turn_context,omitempty"`
	WorldState       json.RawMessage `json:"world_state,omitempty"`
	Data             map[string]any  `json:"data,omitempty"`
}

type RollbackEvent struct {
	NumTurns uint32 `json:"num_turns"`
}

type CompactedEvent struct {
	Message            string          `json:"message"`
	ReplacementHistory json.RawMessage `json:"replacement_history,omitempty"`
	// ReplacementHistoryMetadata is an aligned sidecar for the replacement
	// history's harness metadata (Rust CompactedItemWire
	// replacement_history_metadata, #38058). It is optional; legacy payloads
	// omit it.
	ReplacementHistoryMetadata json.RawMessage `json:"replacement_history_metadata,omitempty"`
	WindowNumber               *uint64         `json:"window_number,omitempty"`
	FirstWindowID              string          `json:"first_window_id,omitempty"`
	PreviousWindowID           string          `json:"previous_window_id,omitempty"`
	WindowID                   string          `json:"window_id,omitempty"`
}

type ThreadGoal struct {
	ThreadID        string `json:"threadId"`
	Objective       string `json:"objective"`
	Status          string `json:"status"`
	TokenBudget     *int64 `json:"tokenBudget,omitempty"`
	TokensUsed      int64  `json:"tokensUsed"`
	TimeUsedSeconds int64  `json:"timeUsedSeconds"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
}

type Recorder struct {
	mu          sync.Mutex
	path        string
	file        *os.File
	paginated   bool
	threadID    string
	nextOrdinal uint64
	afterFlush  func(string)
}

type CreateParams struct {
	CodexHome                   string
	SessionID                   string
	SessionPrefix               string
	ThreadID                    string
	ForkedFromID                string
	Source                      string
	ThreadSource                string
	Originator                  string
	CWD                         string
	Model                       string
	ModelProvider               string
	HistoryMode                 string
	HistoryBase                 *HistoryPosition
	SubagentHistoryStartOrdinal *uint64
	MemoryMode                  string
	ParentThreadID              string
	BaseInstructions            string
	BaseInstructionsProvenance  *session.BaseInstructionsProvenance
	AgentNickname               string
	AgentRole                   string
	AgentPath                   string
	DynamicTools                []json.RawMessage
	SelectedCapabilityRoots     []json.RawMessage
	MultiAgentVersion           string
	ContextWindow               json.RawMessage
	CLIVersion                  string
	Git                         map[string]string
	Extra                       map[string]any
	Now                         time.Time
}

func NewRecorder(params *CreateParams) (*Recorder, error) {
	if params == nil {
		return nil, errors.New("create params are required")
	}
	if params.ThreadID == "" {
		return nil, errors.New("thread id is required")
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		sessionID = params.ThreadID
	}
	now := params.Now
	if now.IsZero() {
		now = time.Now()
	}
	path := PathForThread(params.CodexHome, params.ThreadID, now)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	recorder := &Recorder{
		path:      path,
		file:      file,
		paginated: strings.EqualFold(strings.TrimSpace(params.HistoryMode), "paginated"),
		threadID:  params.ThreadID,
	}
	if recorder.paginated && params.HistoryBase != nil {
		recorder.nextOrdinal = params.HistoryBase.EndOrdinalExclusive
	}
	meta := &SessionMeta{
		ID:                          params.ThreadID,
		SessionID:                   sessionID,
		SessionPrefix:               params.SessionPrefix,
		ForkedFromID:                params.ForkedFromID,
		Timestamp:                   now.UTC().Format(time.RFC3339),
		CWD:                         params.CWD,
		Model:                       params.Model,
		Source:                      params.Source,
		ThreadSource:                params.ThreadSource,
		Originator:                  params.Originator,
		ModelProvider:               params.ModelProvider,
		HistoryMode:                 params.HistoryMode,
		HistoryBase:                 cloneHistoryPosition(params.HistoryBase),
		SubagentHistoryStartOrdinal: cloneUint64Ptr(params.SubagentHistoryStartOrdinal),
		MemoryMode:                  params.MemoryMode,
		ParentThreadID:              params.ParentThreadID,
		BaseInstructions:            params.BaseInstructions,
		BaseInstructionsProvenance:  cloneBaseInstructionsProvenance(params.BaseInstructionsProvenance),
		AgentNickname:               params.AgentNickname,
		AgentRole:                   params.AgentRole,
		AgentPath:                   params.AgentPath,
		DynamicTools:                cloneRawMessages(params.DynamicTools),
		SelectedCapabilityRoots:     cloneRawMessages(params.SelectedCapabilityRoots),
		MultiAgentVersion:           params.MultiAgentVersion,
		ContextWindow:               append(json.RawMessage(nil), params.ContextWindow...),
		CLIVersion:                  params.CLIVersion,
		Git:                         cloneStringMap(params.Git),
		Extra:                       cloneAnyMap(params.Extra),
	}
	if err := recorder.AppendLine(Line{Type: "session_meta", Timestamp: now.UTC().Format(time.RFC3339Nano), Meta: meta}); err != nil {
		file.Close()
		return nil, err
	}
	return recorder, nil
}

func PathForThread(codexHome string, threadID string, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	return filepath.Join(
		codexHome,
		SessionsSubdir,
		now.Format("2006"),
		now.Format("01"),
		now.Format("02"),
		fmt.Sprintf("rollout-%s-%s.jsonl", now.Format("2006-01-02T15-04-05"), threadID),
	)
}

func Resume(path string) (*Recorder, error) {
	lines, _, err := Load(path)
	if err != nil {
		return nil, err
	}
	paginated := false
	threadID := ""
	nextOrdinal := uint64(0)
	for i := range lines {
		if lines[i].Meta != nil && strings.EqualFold(strings.TrimSpace(lines[i].Meta.HistoryMode), "paginated") {
			paginated = true
		}
		if lines[i].Meta != nil && threadID == "" {
			threadID = lines[i].Meta.ID
		}
		if lines[i].Ordinal != nil && *lines[i].Ordinal >= nextOrdinal {
			nextOrdinal = *lines[i].Ordinal + 1
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Recorder{path: path, file: file, paginated: paginated, threadID: threadID, nextOrdinal: nextOrdinal}, nil
}

func (line Line) MarshalJSON() ([]byte, error) {
	if line.Type == "session_meta" && line.Meta != nil {
		return json.Marshal(struct {
			Timestamp string       `json:"timestamp"`
			Ordinal   *uint64      `json:"ordinal,omitempty"`
			Type      string       `json:"type"`
			Payload   *SessionMeta `json:"payload"`
		}{Timestamp: line.Timestamp, Ordinal: line.Ordinal, Type: line.Type, Payload: line.Meta})
	}
	if line.Type == "response_item" && len(line.Item) > 0 {
		if len(line.ItemMetadata) > 0 && string(line.ItemMetadata) != "null" {
			return json.Marshal(struct {
				Timestamp string          `json:"timestamp"`
				Ordinal   *uint64         `json:"ordinal,omitempty"`
				Type      string          `json:"type"`
				Payload   json.RawMessage `json:"payload"`
				Metadata  json.RawMessage `json:"metadata"`
			}{Timestamp: line.Timestamp, Ordinal: line.Ordinal, Type: line.Type, Payload: line.Item, Metadata: line.ItemMetadata})
		}
		return json.Marshal(struct {
			Timestamp string          `json:"timestamp"`
			Ordinal   *uint64         `json:"ordinal,omitempty"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}{Timestamp: line.Timestamp, Ordinal: line.Ordinal, Type: line.Type, Payload: line.Item})
	}
	type lineAlias Line
	return json.Marshal(lineAlias(line))
}

func (r *Recorder) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

func (r *Recorder) IsPaginated() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paginated
}

func (r *Recorder) SetAfterFlush(callback func(string)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.afterFlush = callback
	r.mu.Unlock()
}

func (r *Recorder) AppendItem(item Item) error {
	if r.IsPaginated() {
		return r.appendPaginatedItem(item, time.Now().UTC())
	}
	line, err := LineFromItem(&item, time.Now().UTC())
	if err != nil {
		return err
	}
	return r.AppendLine(*line)
}

// AppendItemStarted persists the canonical lifecycle event emitted before a
// paginated turn item reaches a terminal state.
func (r *Recorder) AppendItemStarted(item json.RawMessage, turnID string, startedAt time.Time) error {
	if len(item) == 0 {
		return errors.New("started item payload is required")
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return errors.New("started item turn id is required")
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(map[string]any{
		"type":          "item_started",
		"thread_id":     r.threadID,
		"turn_id":       turnID,
		"item":          json.RawMessage(item),
		"started_at_ms": startedAt.UTC().UnixMilli(),
	})
	if err != nil {
		return err
	}
	return r.AppendLine(Line{Type: "event_msg", Timestamp: startedAt.UTC().Format(time.RFC3339Nano), Payload: payload})
}

// AppendItemCompleted persists the canonical lifecycle event used by Rust
// paginated rollouts and consumed by the thread-history SQLite projection.
func (r *Recorder) AppendItemCompleted(item json.RawMessage, turnID string, startedAt time.Time, completedAt time.Time) error {
	if len(item) == 0 {
		return errors.New("completed item payload is required")
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return errors.New("completed item turn id is required")
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	payload := map[string]any{
		"type":            "item_completed",
		"thread_id":       r.threadID,
		"turn_id":         turnID,
		"item":            json.RawMessage(item),
		"completed_at_ms": completedAt.UTC().UnixMilli(),
	}
	if !startedAt.IsZero() {
		payload["started_at_ms"] = startedAt.UTC().UnixMilli()
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return r.AppendLine(Line{Type: "event_msg", Timestamp: completedAt.UTC().Format(time.RFC3339Nano), Payload: raw})
}

func (r *Recorder) AppendThreadGoalUpdated(goal ThreadGoal, turnID string, now time.Time) error {
	goal.ThreadID = strings.TrimSpace(goal.ThreadID)
	if goal.ThreadID == "" {
		goal.ThreadID = strings.TrimSpace(r.threadID)
	}
	if goal.ThreadID == "" || strings.TrimSpace(goal.Objective) == "" {
		return errors.New("thread goal payload is invalid")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	payload := struct {
		Type     string     `json:"type"`
		ThreadID string     `json:"threadId"`
		TurnID   string     `json:"turnId,omitempty"`
		Goal     ThreadGoal `json:"goal"`
	}{
		Type: "thread_goal_updated", ThreadID: goal.ThreadID, TurnID: strings.TrimSpace(turnID), Goal: goal,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return r.AppendLine(Line{Type: "event_msg", Timestamp: now.UTC().Format(time.RFC3339Nano), Payload: raw})
}

func (r *Recorder) AppendThreadRolledBack(numTurns uint32, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	payload, err := json.Marshal(struct {
		Type     string `json:"type"`
		NumTurns uint32 `json:"num_turns"`
	}{
		Type:     "thread_rolled_back",
		NumTurns: numTurns,
	})
	if err != nil {
		return err
	}
	return r.AppendLine(Line{
		Type:      "event_msg",
		Timestamp: now.UTC().Format(time.RFC3339Nano),
		Payload:   payload,
	})
}

// AppendThreadSettingsApplied persists the settings snapshot used to restore
// thread-level approval behavior after a cold resume.
func (r *Recorder) AppendThreadSettingsApplied(approvalPolicy string, now time.Time) error {
	approvalPolicy = strings.TrimSpace(approvalPolicy)
	if approvalPolicy == "" {
		return errors.New("approval policy is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	payload, err := json.Marshal(struct {
		Type           string `json:"type"`
		ThreadSettings struct {
			ApprovalPolicy string `json:"approval_policy"`
		} `json:"thread_settings"`
	}{
		Type: "thread_settings_applied",
		ThreadSettings: struct {
			ApprovalPolicy string `json:"approval_policy"`
		}{ApprovalPolicy: approvalPolicy},
	})
	if err != nil {
		return err
	}
	return r.AppendLine(Line{Type: "event_msg", Timestamp: now.UTC().Format(time.RFC3339Nano), Payload: payload})
}

func (r *Recorder) AppendTurnStarted(turnID string, startedAt time.Time) error {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(struct {
		Type      string `json:"type"`
		TurnID    string `json:"turn_id"`
		StartedAt int64  `json:"started_at"`
	}{
		Type:      "task_started",
		TurnID:    turnID,
		StartedAt: startedAt.UTC().Unix(),
	})
	if err != nil {
		return err
	}
	return r.AppendLine(Line{
		Type:      "event_msg",
		Timestamp: startedAt.UTC().Format(time.RFC3339Nano),
		Payload:   payload,
	})
}

func (r *Recorder) AppendTurnComplete(turnID string, completedAt time.Time, durationMS int64) error {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(struct {
		Type        string `json:"type"`
		TurnID      string `json:"turn_id"`
		CompletedAt int64  `json:"completed_at"`
		DurationMS  int64  `json:"duration_ms"`
	}{
		Type:        "task_complete",
		TurnID:      turnID,
		CompletedAt: completedAt.UTC().Unix(),
		DurationMS:  durationMS,
	})
	if err != nil {
		return err
	}
	return r.AppendLine(Line{
		Type:      "event_msg",
		Timestamp: completedAt.UTC().Format(time.RFC3339Nano),
		Payload:   payload,
	})
}

func (r *Recorder) AppendTurnError(message string, now time.Time) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	payload, err := json.Marshal(struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}{
		Type:    "error",
		Message: message,
	})
	if err != nil {
		return err
	}
	return r.AppendLine(Line{
		Type:      "event_msg",
		Timestamp: now.UTC().Format(time.RFC3339Nano),
		Payload:   payload,
	})
}

func (r *Recorder) AppendTurnAborted(turnID string, reason string, completedAt time.Time, durationMS int64) error {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "interrupted"
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(struct {
		Type        string `json:"type"`
		TurnID      string `json:"turn_id"`
		Reason      string `json:"reason"`
		CompletedAt int64  `json:"completed_at"`
		DurationMS  int64  `json:"duration_ms"`
	}{
		Type:        "turn_aborted",
		TurnID:      turnID,
		Reason:      reason,
		CompletedAt: completedAt.UTC().Unix(),
		DurationMS:  durationMS,
	})
	if err != nil {
		return err
	}
	return r.AppendLine(Line{
		Type:      "event_msg",
		Timestamp: completedAt.UTC().Format(time.RFC3339Nano),
		Payload:   payload,
	})
}

func (r *Recorder) AppendCompacted(message string, replacement []Item, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	history := make([]json.RawMessage, 0, len(replacement))
	var historyMetadata []json.RawMessage
	for i := range replacement {
		item := replacement[i]
		line, err := LineFromItem(&item, now)
		if err != nil {
			return err
		}
		raw := line.Item
		if len(raw) == 0 {
			data, marshalErr := json.Marshal(item)
			if marshalErr != nil {
				return marshalErr
			}
			raw = data
		}
		history = append(history, append(json.RawMessage(nil), raw...))
		// Rust #38058: carry harness metadata in an aligned sidecar so the
		// persisted replacement-history payload shape stays backward
		// compatible. Metadata missing on an item is stored as an empty
		// object so the sidecar stays aligned.
		if metadata, ok := rolloutItemHarnessMetadata(item); ok {
			historyMetadata = append(historyMetadata, append(json.RawMessage(nil), metadata...))
		} else if len(historyMetadata) > 0 || i > 0 {
			historyMetadata = append(historyMetadata, json.RawMessage(`{}`))
		}
	}
	payloadValues := struct {
		Message                    string            `json:"message"`
		ReplacementHistory         []json.RawMessage `json:"replacement_history,omitempty"`
		ReplacementHistoryMetadata []json.RawMessage `json:"replacement_history_metadata,omitempty"`
	}{
		Message:                    strings.TrimSpace(message),
		ReplacementHistory:         history,
		ReplacementHistoryMetadata: historyMetadata,
	}
	payload, err := json.Marshal(payloadValues)
	if err != nil {
		return err
	}
	return r.AppendLine(Line{
		Type:      "compacted",
		Timestamp: now.UTC().Format(time.RFC3339Nano),
		Payload:   payload,
	})
}

// rolloutItemHarnessMetadata returns the harness-owned metadata carried on a
// replacement history item (Rust CodexHarnessMetadata, #38058). It is stored
// out-of-band in the item's data map and never included in the model-visible
// response item payload.
func rolloutItemHarnessMetadata(item Item) (json.RawMessage, bool) {
	if len(item.Data) == 0 {
		return nil, false
	}
	switch value := item.Data["harness_metadata"].(type) {
	case json.RawMessage:
		if len(value) > 0 && string(value) != "null" {
			return value, true
		}
	case string:
		if strings.TrimSpace(value) != "" {
			return json.RawMessage(value), true
		}
	}
	return nil, false
}

func LineFromItem(item *Item, now time.Time) (*Line, error) {
	if item == nil {
		return nil, errors.New("rollout item is nil")
	}
	raw := item.Raw
	if len(raw) > 0 {
		var object map[string]any
		if json.Unmarshal(raw, &object) == nil && !legacyRolloutItemWrapper(object) {
			stampResponseItemTurnID(object, item)
			if encoded, marshalErr := json.Marshal(object); marshalErr == nil {
				raw = encoded
			}
		}
	}
	if len(raw) == 0 || legacyRolloutRawItem(raw) {
		payload, ok := canonicalResponseItemPayload(item)
		if !ok {
			payload = item
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		raw = data
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	line := &Line{
		Type:       "response_item",
		Timestamp:  now.UTC().Format(time.RFC3339Nano),
		Item:       append(json.RawMessage(nil), raw...),
		ItemID:     item.ID,
		Role:       item.Role,
		ResponseID: item.ResponseID,
		Data:       cloneAnyMap(item.Data),
	}
	if raw, ok := item.Data["harness_metadata"].(json.RawMessage); ok {
		line.ItemMetadata = append(json.RawMessage(nil), raw...)
	} else if raw, ok := item.Data["harness_metadata"].(string); ok && strings.TrimSpace(raw) != "" {
		line.ItemMetadata = json.RawMessage(raw)
	}
	if turnID, ok := item.Metadata["turnId"].(string); ok {
		line.TurnID = turnID
	}
	if line.TurnID == "" {
		if turnID, ok := item.Metadata["turn_id"].(string); ok {
			line.TurnID = turnID
		}
	}
	return line, nil
}

func (r *Recorder) AppendLine(line Line) error {
	if r == nil || r.file == nil {
		return errors.New("rollout recorder is closed")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.paginated {
		if line.Ordinal == nil {
			ordinal := r.nextOrdinal
			line.Ordinal = &ordinal
		} else if *line.Ordinal != r.nextOrdinal {
			return fmt.Errorf("paginated rollout expected ordinal %d, got %d", r.nextOrdinal, *line.Ordinal)
		}
	}
	data, err := json.Marshal(line)
	if err != nil {
		return err
	}
	if _, err := r.file.Write(append(data, '\n')); err != nil {
		return err
	}
	if r.paginated {
		r.nextOrdinal++
	}
	return nil
}

func (r *Recorder) Flush() error {
	r.mu.Lock()
	if r.file == nil {
		r.mu.Unlock()
		return nil
	}
	err := r.file.Sync()
	path, callback := r.path, r.afterFlush
	r.mu.Unlock()
	if err == nil && callback != nil {
		callback(path)
	}
	return err
}

func (r *Recorder) Close() error {
	r.mu.Lock()
	if r.file == nil {
		r.mu.Unlock()
		return nil
	}
	syncErr := r.file.Sync()
	closeErr := r.file.Close()
	r.file = nil
	path, callback := r.path, r.afterFlush
	r.mu.Unlock()
	if syncErr == nil && callback != nil {
		callback(path)
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func cloneHistoryPosition(value *HistoryPosition) *HistoryPosition {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneUint64Ptr(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func Load(path string) ([]Line, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	var reader io.Reader = file
	if strings.HasSuffix(strings.ToLower(path), ".zst") {
		decoder, decodeErr := zstd.NewReader(file)
		if decodeErr != nil {
			return nil, 0, decodeErr
		}
		defer decoder.Close()
		reader = decoder
	}
	var lines []Line
	parseErrors := 0
	lineReader := bufio.NewReader(reader)
	for {
		raw, readErr := lineReader.ReadBytes('\n')
		if readErr != nil && len(raw) == 0 {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return lines, parseErrors, readErr
		}
		raw = bytes.TrimRight(raw, "\r\n")
		var line Line
		if err := unmarshalLine(raw, &line); err != nil {
			parseErrors++
		} else {
			lines = append(lines, line)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return lines, parseErrors, readErr
		}
	}
	return lines, parseErrors, nil
}

// EnsureRustCompatibleSessionMeta makes legacy Go rollouts readable by the
// Rust ThreadStore used by the Desktop app. Older Go rollouts omitted required
// string fields when they were empty, so Rust discarded their session_meta
// record. Appending a complete metadata record is safe for an active rollout
// and avoids rewriting a file that another recorder may still have open.
func EnsureRustCompatibleSessionMeta(path string) (string, error) {
	existing, ok := ExistingRolloutPath(path)
	if !ok {
		return "", os.ErrNotExist
	}
	if strings.HasSuffix(strings.ToLower(existing), ".zst") {
		var err error
		existing, err = MaterializeRolloutForReference(existing)
		if err != nil {
			return "", err
		}
	}
	compatible, err := hasRustCompatibleSessionMeta(existing)
	if err != nil {
		return "", err
	}
	if compatible {
		return existing, nil
	}
	meta, err := FirstSessionMeta(existing)
	if err != nil {
		return "", err
	}
	if meta == nil || strings.TrimSpace(meta.ID) == "" {
		return "", errors.New("rollout session metadata is missing a thread id")
	}
	compatibilityMeta := *meta
	if strings.TrimSpace(compatibilityMeta.SessionID) == "" {
		compatibilityMeta.SessionID = compatibilityMeta.ID
	}
	now := time.Now().UTC()
	if strings.TrimSpace(compatibilityMeta.Timestamp) == "" {
		compatibilityMeta.Timestamp = now.Format(time.RFC3339)
	}
	encoded, err := json.Marshal(Line{
		Type:      "session_meta",
		Timestamp: now.Format(time.RFC3339Nano),
		Meta:      &compatibilityMeta,
	})
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(existing, os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if info, statErr := file.Stat(); statErr != nil {
		return "", statErr
	} else if info.Size() > 0 {
		last := make([]byte, 1)
		if _, readErr := file.ReadAt(last, info.Size()-1); readErr != nil {
			return "", readErr
		}
		if last[0] != '\n' {
			encoded = append([]byte{'\n'}, encoded...)
		}
	}
	encoded = append(encoded, '\n')
	if _, err := file.Write(encoded); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	return existing, nil
}

func hasRustCompatibleSessionMeta(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		raw, readErr := reader.ReadBytes('\n')
		if len(raw) > 0 {
			var envelope struct {
				Type    string                     `json:"type"`
				Payload map[string]json.RawMessage `json:"payload"`
			}
			if json.Unmarshal(bytes.TrimSpace(raw), &envelope) == nil && envelope.Type == "session_meta" {
				if rustRequiredSessionMetaStrings(envelope.Payload) {
					return true, nil
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return false, nil
			}
			return false, readErr
		}
	}
}

func rustRequiredSessionMetaStrings(payload map[string]json.RawMessage) bool {
	for _, key := range []string{"session_id", "id", "timestamp", "cwd", "originator", "cli_version"} {
		raw, ok := payload[key]
		if !ok {
			return false
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return false
		}
	}
	return true
}

// PlainRolloutPath returns the canonical state-DB path for either a plain or compressed rollout.
func PlainRolloutPath(path string) string {
	if strings.HasSuffix(strings.ToLower(path), ".jsonl.zst") {
		return strings.TrimSuffix(path, filepath.Ext(path))
	}
	return path
}

// ExistingRolloutPath resolves the canonical plain state-DB path to the
// rollout file that currently exists, including its compressed representation.
func ExistingRolloutPath(path string) (string, bool) {
	plain := PlainRolloutPath(strings.TrimSpace(path))
	if plain == "" {
		return "", false
	}
	for _, candidate := range []string{plain, plain + ".zst"} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, true
		}
	}
	return "", false
}

// MaterializeRolloutForReference restores a compressed rollout to its
// canonical JSONL path before a HistoryPosition records physical byte offsets.
func MaterializeRolloutForReference(path string) (string, error) {
	materializeReferenceMu.Lock()
	defer materializeReferenceMu.Unlock()

	plain := PlainRolloutPath(strings.TrimSpace(path))
	if plain == "" {
		return "", errors.New("rollout path is required")
	}
	if info, err := os.Stat(plain); err == nil && info.Mode().IsRegular() {
		return plain, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	compressed := plain + ".zst"
	metadata, err := os.Stat(compressed)
	if errors.Is(err, os.ErrNotExist) {
		return plain, nil
	}
	if err != nil {
		return "", err
	}
	if parent := filepath.Dir(plain); parent != "" && parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", err
		}
	}
	temporary, err := os.CreateTemp(filepath.Dir(plain), "."+filepath.Base(plain)+".decompress-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(metadata.Mode().Perm()); err != nil {
		return "", err
	}
	input, err := os.Open(compressed)
	if err != nil {
		return "", err
	}
	decoder, err := zstd.NewReader(input)
	if err != nil {
		_ = input.Close()
		return "", err
	}
	_, copyErr := io.Copy(temporary, decoder)
	decoder.Close()
	closeInputErr := input.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeInputErr != nil {
		return "", closeInputErr
	}
	if err := temporary.Sync(); err != nil {
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Chtimes(temporaryPath, metadata.ModTime(), metadata.ModTime()); err != nil {
		return "", err
	}
	if err := os.Link(temporaryPath, plain); err == nil {
		published = true
	} else if _, statErr := os.Stat(plain); statErr == nil {
		published = true
	} else {
		destination, createErr := os.OpenFile(plain, os.O_CREATE|os.O_EXCL|os.O_WRONLY, metadata.Mode().Perm())
		if createErr != nil {
			return "", createErr
		}
		source, openErr := os.Open(temporaryPath)
		if openErr != nil {
			_ = destination.Close()
			return "", openErr
		}
		_, copyErr = io.Copy(destination, source)
		_ = source.Close()
		if copyErr == nil {
			copyErr = destination.Sync()
		}
		if closeErr := destination.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			_ = os.Remove(plain)
			return "", copyErr
		}
		published = true
	}
	if published {
		if err := os.Chtimes(plain, metadata.ModTime(), metadata.ModTime()); err != nil {
			return "", err
		}
		if err := os.Remove(compressed); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return plain, nil
}

// RolloutByteLength reports the uncompressed JSONL length used by persisted
// HistoryPosition byte offsets.
func RolloutByteLength(path string) (uint64, error) {
	existing, ok := ExistingRolloutPath(path)
	if !ok {
		return 0, os.ErrNotExist
	}
	if !strings.HasSuffix(strings.ToLower(existing), ".zst") {
		info, err := os.Stat(existing)
		if err != nil {
			return 0, err
		}
		return uint64(info.Size()), nil
	}
	file, err := os.Open(existing)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	decoder, err := zstd.NewReader(file)
	if err != nil {
		return 0, err
	}
	defer decoder.Close()
	count, err := io.Copy(io.Discard, decoder)
	if err != nil {
		return 0, err
	}
	return uint64(count), nil
}

func unmarshalLine(data []byte, line *Line) error {
	if err := json.Unmarshal(data, line); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		if metadata, ok := raw["metadata"]; ok && len(metadata) > 0 && string(metadata) != "null" && len(line.ItemMetadata) == 0 {
			line.ItemMetadata = append(json.RawMessage(nil), metadata...)
		}
		if payload, ok := raw["payload"]; ok {
			if len(line.Payload) == 0 {
				line.Payload = append(json.RawMessage(nil), payload...)
			}
			switch line.Type {
			case "session_meta":
				if line.Meta == nil {
					var meta SessionMeta
					if err := json.Unmarshal(payload, &meta); err == nil && meta.ID != "" {
						line.Meta = &meta
					}
				}
			case "response_item":
				if len(line.Item) == 0 {
					line.Item = append(json.RawMessage(nil), payload...)
				}
			case "event_msg":
				if line.ThreadRolledBack == nil {
					line.ThreadRolledBack = rollbackEventFromPayload(payload)
				}
			case "compacted":
				line.Payload = append(json.RawMessage(nil), payload...)
			case "turn_context":
				line.TurnContext = append(json.RawMessage(nil), payload...)
			case "world_state":
				line.WorldState = append(json.RawMessage(nil), payload...)
			}
		}
		if line.ThreadRolledBack == nil {
			if payload, ok := raw["thread_rolled_back"]; ok {
				line.ThreadRolledBack = rollbackEventFromPayload(payload)
			}
		}
		if len(line.TurnContext) == 0 {
			if payload, ok := raw["turn_context"]; ok {
				line.TurnContext = append(json.RawMessage(nil), payload...)
			}
		}
		if len(line.WorldState) == 0 {
			if payload, ok := raw["world_state"]; ok {
				line.WorldState = append(json.RawMessage(nil), payload...)
			}
		}
	}
	if line.Type == "response_item" {
		line.Type = "item"
	}
	return nil
}

func compactedEventFromPayload(data json.RawMessage) *CompactedEvent {
	if len(data) == 0 {
		return nil
	}
	var event CompactedEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil
	}
	if len(event.ReplacementHistory) == 0 && strings.TrimSpace(event.Message) == "" {
		return nil
	}
	return &event
}

func rollbackEventFromPayload(data json.RawMessage) *RollbackEvent {
	if len(data) == 0 {
		return nil
	}
	var tagged struct {
		Type          string  `json:"type"`
		NumTurns      *uint32 `json:"num_turns"`
		NumTurnsCamel *uint32 `json:"numTurns"`
	}
	if err := json.Unmarshal(data, &tagged); err == nil {
		switch strings.ToLower(tagged.Type) {
		case "thread_rolled_back", "threadrolledback":
			if tagged.NumTurns != nil {
				return &RollbackEvent{NumTurns: *tagged.NumTurns}
			}
			if tagged.NumTurnsCamel != nil {
				return &RollbackEvent{NumTurns: *tagged.NumTurnsCamel}
			}
		}
	}
	if tagged.Type != "" {
		return nil
	}
	var direct struct {
		NumTurns      *uint32 `json:"num_turns"`
		NumTurnsCamel *uint32 `json:"numTurns"`
	}
	if err := json.Unmarshal(data, &direct); err == nil {
		switch {
		case direct.NumTurns != nil:
			return &RollbackEvent{NumTurns: *direct.NumTurns}
		case direct.NumTurnsCamel != nil:
			return &RollbackEvent{NumTurns: *direct.NumTurnsCamel}
		}
	}
	var variant struct {
		ThreadRolledBack *struct {
			NumTurns      *uint32 `json:"num_turns"`
			NumTurnsCamel *uint32 `json:"numTurns"`
		} `json:"ThreadRolledBack"`
		ThreadRolledBackSnake *struct {
			NumTurns      *uint32 `json:"num_turns"`
			NumTurnsCamel *uint32 `json:"numTurns"`
		} `json:"thread_rolled_back"`
	}
	if err := json.Unmarshal(data, &variant); err == nil {
		if variant.ThreadRolledBack != nil {
			if variant.ThreadRolledBack.NumTurns != nil {
				return &RollbackEvent{NumTurns: *variant.ThreadRolledBack.NumTurns}
			}
			if variant.ThreadRolledBack.NumTurnsCamel != nil {
				return &RollbackEvent{NumTurns: *variant.ThreadRolledBack.NumTurnsCamel}
			}
		}
		if variant.ThreadRolledBackSnake != nil {
			if variant.ThreadRolledBackSnake.NumTurns != nil {
				return &RollbackEvent{NumTurns: *variant.ThreadRolledBackSnake.NumTurns}
			}
			if variant.ThreadRolledBackSnake.NumTurnsCamel != nil {
				return &RollbackEvent{NumTurns: *variant.ThreadRolledBackSnake.NumTurnsCamel}
			}
		}
	}
	return nil
}

func ItemsFromLines(lines []Line) []Item {
	items := make([]Item, 0, len(lines))
	for i := range lines {
		if lines[i].ThreadRolledBack != nil {
			items = rollbackItems(items, int(lines[i].ThreadRolledBack.NumTurns))
			continue
		}
		if lines[i].Type == "compacted" {
			if replacement, ok := compactedReplacementItems(lines[i].Payload); ok {
				items = replacement
			}
			continue
		}
		if item, ok := ItemFromLine(&lines[i]); ok {
			items = append(items, *item)
		}
	}
	return items
}

func compactedReplacementItems(payload json.RawMessage) ([]Item, bool) {
	event := compactedEventFromPayload(payload)
	if event == nil || len(event.ReplacementHistory) == 0 {
		return nil, false
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(event.ReplacementHistory, &rawItems); err != nil {
		return nil, false
	}
	var historyMetadata []json.RawMessage
	if len(event.ReplacementHistoryMetadata) > 0 {
		_ = json.Unmarshal(event.ReplacementHistoryMetadata, &historyMetadata)
	}
	items := make([]Item, 0, len(rawItems))
	for i := range rawItems {
		line := Line{Type: "item", Item: rawItems[i]}
		item, ok := ItemFromLine(&line)
		if !ok {
			continue
		}
		// Rust #38058: restore the aligned harness metadata sidecar so it
		// survives compaction/truncation and later resume/fork replay.
		if i < len(historyMetadata) && len(historyMetadata[i]) > 0 && string(historyMetadata[i]) != "null" && string(historyMetadata[i]) != "{}" {
			if item.Data == nil {
				item.Data = map[string]any{}
			}
			item.Data["harness_metadata"] = append(json.RawMessage(nil), historyMetadata[i]...)
		}
		items = append(items, *item)
	}
	return items, true
}

func rollbackItems(items []Item, numTurns int) []Item {
	if numTurns <= 0 || len(items) == 0 {
		return append([]Item(nil), items...)
	}
	turnIDs := make([]string, 0)
	seen := make(map[string]bool)
	for i := range items {
		turnID := itemTurnID(&items[i], i)
		if seen[turnID] {
			continue
		}
		seen[turnID] = true
		turnIDs = append(turnIDs, turnID)
	}
	if numTurns >= len(turnIDs) {
		return []Item{}
	}
	drop := make(map[string]bool)
	for _, turnID := range turnIDs[len(turnIDs)-numTurns:] {
		drop[turnID] = true
	}
	out := make([]Item, 0, len(items))
	for i := range items {
		if drop[itemTurnID(&items[i], i)] {
			continue
		}
		out = append(out, items[i])
	}
	return out
}

func itemTurnID(item *Item, index int) string {
	if item != nil {
		if value, ok := item.Metadata["turnId"].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
		if value, ok := item.Metadata["turn_id"].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
		if value, ok := item.Data["turnId"].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
		if value, ok := item.Data["turn_id"].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return fmt.Sprintf("turn-%d", index+1)
}

func ItemFromLine(line *Line) (*Item, bool) {
	if line == nil || line.Type != "item" {
		return nil, false
	}
	var item Item
	if len(line.Item) > 0 {
		if err := json.Unmarshal(line.Item, &item); err != nil {
			item = Item{Raw: append(json.RawMessage(nil), line.Item...)}
		}
		item.Raw = append(json.RawMessage(nil), line.Item...)
	}
	if item.Type == "" {
		if value, ok := line.Data["type"].(string); ok {
			item.Type = value
		}
	}
	if item.ID == "" {
		item.ID = line.ItemID
	}
	if item.Role == "" {
		item.Role = line.Role
	}
	if item.ResponseID == "" {
		item.ResponseID = line.ResponseID
	}
	if item.Data == nil {
		item.Data = cloneAnyMap(line.Data)
	}
	if len(line.ItemMetadata) > 0 {
		if item.Data == nil {
			item.Data = map[string]any{}
		}
		item.Data["harness_metadata"] = append(json.RawMessage(nil), line.ItemMetadata...)
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	if line.TurnID != "" {
		item.Metadata["turnId"] = line.TurnID
	}
	if turnID := responseItemTurnID(item.Raw); turnID != "" {
		item.Metadata["turnId"] = turnID
	}
	return &item, item.Type != ""
}

func InputItemsFromLines(lines []Line, includeToolOutputs bool) []any {
	return InputItemsFromItems(ItemsFromLines(lines), includeToolOutputs)
}

func InputItemsFromItems(items []Item, includeToolOutputs bool) []any {
	out := make([]any, 0, len(items))
	for i := range items {
		if input := inputItemFromItem(&items[i], includeToolOutputs); input != nil {
			out = append(out, input)
		}
	}
	return out
}

func inputItemFromItem(item *Item, includeToolOutputs bool) any {
	if item == nil {
		return nil
	}
	if len(item.Raw) > 0 {
		var raw any
		if err := json.Unmarshal(item.Raw, &raw); err == nil {
			if object, ok := raw.(map[string]any); ok {
				if legacyRolloutItemWrapper(object) {
					return inputItemFromStructuredItem(item, includeToolOutputs)
				}
				delete(object, "internal_chat_message_metadata_passthrough")
			}
			return raw
		}
	}
	return inputItemFromStructuredItem(item, includeToolOutputs)
}

func inputItemFromStructuredItem(item *Item, includeToolOutputs bool) any {
	switch item.Type {
	case "message", "user_message", "agent_message", "assistant_message":
		return messageInputItem(item)
	case "function_call", "custom_tool_call", "tool_search_call":
		return toolCallInputItem(item)
	case "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_output":
		if !includeToolOutputs {
			return nil
		}
		return toolOutputInputItem(item)
	default:
		if strings.TrimSpace(item.Text) == "" {
			return nil
		}
		return messageInputItem(item)
	}
}

func canonicalResponseItemPayload(item *Item) (any, bool) {
	if item == nil {
		return nil, false
	}
	var payload any
	switch item.Type {
	case "message", "user_message", "agent_message", "assistant_message":
		payload = messageInputItem(item)
	case "function_call", "custom_tool_call", "tool_search_call":
		payload = toolCallInputItem(item)
	case "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_output":
		payload = toolOutputInputItem(item)
	default:
		return nil, false
	}
	object, ok := payload.(map[string]any)
	if !ok || object == nil {
		return nil, false
	}
	if strings.TrimSpace(item.ID) != "" {
		object["id"] = item.ID
	}
	if object["type"] == "message" {
		if phase := firstNonEmpty(stringValue(item.Data, "phase"), stringValue(item.Metadata, "phase")); phase != "" {
			object["phase"] = phase
		}
	}
	stampResponseItemTurnID(object, item)
	return object, true
}

func stampResponseItemTurnID(object map[string]any, item *Item) {
	if object == nil || item == nil {
		return
	}
	turnID := firstNonEmpty(stringValue(item.Metadata, "turnId"), stringValue(item.Metadata, "turn_id"), stringValue(item.Data, "turnId"), stringValue(item.Data, "turn_id"))
	if turnID == "" {
		return
	}
	metadata, _ := object["internal_chat_message_metadata_passthrough"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	}
	if strings.TrimSpace(stringValue(metadata, "turn_id")) == "" {
		metadata["turn_id"] = turnID
	}
	object["internal_chat_message_metadata_passthrough"] = metadata
}

func responseItemTurnID(raw json.RawMessage) string {
	var object map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &object) != nil {
		return ""
	}
	metadata, _ := object["internal_chat_message_metadata_passthrough"].(map[string]any)
	return firstNonEmpty(stringValue(metadata, "turn_id"), stringValue(metadata, "turnId"))
}

func legacyRolloutRawItem(raw json.RawMessage) bool {
	var object map[string]any
	return len(raw) > 0 && json.Unmarshal(raw, &object) == nil && legacyRolloutItemWrapper(object)
}

func legacyRolloutItemWrapper(object map[string]any) bool {
	if object == nil {
		return false
	}
	for _, key := range []string{"metadata", "data", "raw", "response_id"} {
		if _, ok := object[key]; ok {
			return true
		}
	}
	itemType := strings.TrimSpace(stringValue(object, "type"))
	_, hasText := object["text"]
	return hasText && (itemType == "message" || itemType == "user_message" || itemType == "agent_message" || itemType == "assistant_message")
}

type ThreadItem struct {
	Path             string
	ThreadID         string
	Preview          string
	FirstUserMessage string
	CWD              string
	Source           string
	HistoryMode      string
	ParentThreadID   string
	AgentNickname    string
	AgentRole        string
	Model            string
	ModelProvider    string
	CLIVersion       string
	LastResponseID   string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	RecencyAt        time.Time
	Archived         bool
}

type Page struct {
	Items           []ThreadItem
	NextCursor      string
	NumScannedFiles int
	ReachedScanCap  bool
}

type ListOptions struct {
	PageSize       int
	Cursor         string
	SortKey        SortKey
	SortDirection  SortDirection
	Archived       bool
	AllowedSources []string
	ModelProviders []string
	CWDFilters     []string
	Search         string
}

func ListThreads(codexHome string, options ListOptions) (*Page, error) {
	root := filepath.Join(codexHome, SessionsSubdir)
	if options.Archived {
		root = filepath.Join(codexHome, ArchivedSessionsSubdir)
	}
	paths, err := collectRolloutPaths(root)
	if err != nil {
		return nil, err
	}
	items := make([]ThreadItem, 0, len(paths))
	for _, path := range paths {
		item, ok := BuildThreadItem(path, options.Archived)
		if !ok {
			continue
		}
		if !matchesOptions(&item, &options) {
			continue
		}
		items = append(items, item)
	}
	sortThreadItems(items, options.SortKey, options.SortDirection)
	start, err := parseCursor(options.Cursor)
	if err != nil {
		return nil, err
	}
	if start > len(items) {
		start = len(items)
	}
	pageSize := options.PageSize
	if pageSize <= 0 || pageSize > len(items)-start {
		pageSize = len(items) - start
	}
	end := start + pageSize
	nextCursor := ""
	if end < len(items) {
		nextCursor = strconv.Itoa(end)
	}
	return &Page{
		Items:           append([]ThreadItem(nil), items[start:end]...),
		NextCursor:      nextCursor,
		NumScannedFiles: len(paths),
	}, nil
}

func BuildThreadItem(path string, archived bool) (ThreadItem, bool) {
	lines, _, err := Load(path)
	if err != nil || len(lines) == 0 {
		return ThreadItem{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return ThreadItem{}, false
	}
	item := ThreadItem{
		Path:      path,
		UpdatedAt: info.ModTime().UTC(),
		RecencyAt: info.ModTime().UTC(),
		Archived:  archived,
	}
	for _, line := range lines {
		if line.Meta != nil {
			item.ThreadID = line.Meta.ID
			item.CWD = line.Meta.CWD
			item.Source = line.Meta.Source
			item.HistoryMode = line.Meta.HistoryMode
			item.ParentThreadID = line.Meta.ParentThreadID
			item.AgentNickname = line.Meta.AgentNickname
			item.AgentRole = line.Meta.AgentRole
			item.Model = line.Meta.Model
			item.ModelProvider = line.Meta.ModelProvider
			item.CLIVersion = line.Meta.CLIVersion
			if created, err := time.Parse(time.RFC3339, line.Meta.Timestamp); err == nil {
				item.CreatedAt = created.UTC()
			}
			continue
		}
		if !lineItemTime(line).IsZero() {
			item.RecencyAt = lineItemTime(line)
		}
		if line.ResponseID != "" {
			item.LastResponseID = line.ResponseID
		}
		if item.FirstUserMessage == "" {
			item.FirstUserMessage = userTextFromLine(&line)
			item.Preview = item.FirstUserMessage
		}
	}
	if item.CreatedAt.IsZero() {
		if created, ok := ParseTimestampFromFilename(filepath.Base(path)); ok {
			item.CreatedAt = created
		} else {
			item.CreatedAt = item.UpdatedAt
		}
	}
	if item.RecencyAt.IsZero() {
		item.RecencyAt = item.UpdatedAt
	}
	return item, true
}

func ThreadIDFromPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("rollout path is required")
	}
	if threadID, ok := ThreadIDFromFilename(filepath.Base(path)); ok {
		return threadID, nil
	}
	lines, _, err := Load(path)
	if err != nil {
		return "", err
	}
	for _, line := range lines {
		if line.Meta == nil {
			continue
		}
		if threadID := strings.TrimSpace(line.Meta.ID); threadID != "" {
			return threadID, nil
		}
	}
	return "", fmt.Errorf("rollout thread id not found: %s", path)
}

const rolloutTimestampLayout = "2006-01-02T15-04-05"

// ThreadIDFromFilename extracts the thread id embedded in a standard rollout
// filename (rollout-<timestamp>-<threadID>.jsonl[.zst]) without opening the
// file. Standard rollouts always encode the thread id in the filename, so
// callers can locate a thread by scanning filenames instead of reading every
// rollout, which is prohibitively slow for homes with thousands of sessions.
func ThreadIDFromFilename(name string) (string, bool) {
	base := strings.TrimSuffix(filepath.Base(name), ".zst")
	base = strings.TrimSuffix(base, ".jsonl")
	if !strings.HasPrefix(base, "rollout-") {
		return "", false
	}
	rest := strings.TrimPrefix(base, "rollout-")
	if len(rest) <= len(rolloutTimestampLayout) {
		return "", false
	}
	if _, err := time.Parse(rolloutTimestampLayout, rest[:len(rolloutTimestampLayout)]); err != nil {
		return "", false
	}
	threadID := strings.TrimPrefix(rest[len(rolloutTimestampLayout):], "-")
	if strings.TrimSpace(threadID) == "" {
		return "", false
	}
	return threadID, true
}

func ParseTimestampFromFilename(name string) (time.Time, bool) {
	name = strings.TrimPrefix(name, "rollout-")
	if len(name) < len(rolloutTimestampLayout) {
		return time.Time{}, false
	}
	stamp := name[:len(rolloutTimestampLayout)]
	created, err := time.Parse(rolloutTimestampLayout, stamp)
	if err != nil {
		return time.Time{}, false
	}
	return created.UTC(), true
}

func Archive(path string, codexHome string) (string, error) {
	return moveRollout(path, filepath.Join(codexHome, ArchivedSessionsSubdir))
}

func Unarchive(path string, codexHome string) (string, error) {
	createdAt, ok := ParseTimestampFromFilename(filepath.Base(PlainRolloutPath(path)))
	if !ok {
		return "", fmt.Errorf("rollout path %q missing filename timestamp", path)
	}
	targetDir := filepath.Join(
		codexHome,
		SessionsSubdir,
		createdAt.Format("2006"),
		createdAt.Format("01"),
		createdAt.Format("02"),
	)
	target, err := moveRollout(path, targetDir)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if err := os.Chtimes(target, now, now); err != nil {
		return "", err
	}
	return target, nil
}

func Delete(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("rollout path is required")
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("rollout not found: %s", path)
	} else if err != nil {
		return err
	}
	return nil
}

func FindThreadPath(codexHome string, threadID string, archived bool) (string, error) {
	root := filepath.Join(codexHome, SessionsSubdir)
	if archived {
		root = filepath.Join(codexHome, ArchivedSessionsSubdir)
	}
	paths, err := collectRolloutPaths(root)
	if err != nil {
		return "", err
	}
	var unparsed []string
	for _, path := range paths {
		if id, ok := ThreadIDFromFilename(filepath.Base(path)); ok {
			if id == threadID {
				return path, nil
			}
			continue
		}
		unparsed = append(unparsed, path)
	}
	for _, path := range unparsed {
		item, ok := BuildThreadItem(path, archived)
		if ok && item.ThreadID == threadID {
			return path, nil
		}
	}
	return "", fmt.Errorf("rollout thread not found: %s", threadID)
}

func moveRollout(path string, targetDir string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("rollout path is required")
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(targetDir, filepath.Base(path))
	if filepath.Clean(path) == filepath.Clean(target) {
		return target, nil
	}
	if err := os.Rename(path, target); err != nil {
		return "", err
	}
	return target, nil
}

func matchesOptions(item *ThreadItem, options *ListOptions) bool {
	if len(options.AllowedSources) > 0 && !contains(options.AllowedSources, item.Source) {
		return false
	}
	if len(options.ModelProviders) > 0 && !contains(options.ModelProviders, item.ModelProvider) {
		return false
	}
	if len(options.CWDFilters) > 0 && !contains(options.CWDFilters, item.CWD) {
		return false
	}
	if options.Search != "" {
		needle := strings.ToLower(options.Search)
		return strings.Contains(strings.ToLower(item.Preview), needle) ||
			strings.Contains(strings.ToLower(item.FirstUserMessage), needle)
	}
	return true
}

func collectRolloutPaths(root string) ([]string, error) {
	return CollectRolloutPaths(root)
}

// CollectRolloutPaths recursively returns plain and zstd-compressed rollout
// files. Both the legacy flat layout and the date-nested layout are accepted.
func CollectRolloutPaths(root string) ([]string, error) {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	paths := []string{}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		name := strings.ToLower(entry.Name())
		if strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.zst") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

func sortThreadItems(items []ThreadItem, key SortKey, direction SortDirection) {
	if key == "" {
		key = SortCreatedAt
	}
	if direction == "" {
		direction = SortDesc
	}
	sort.SliceStable(items, func(i int, j int) bool {
		left := threadSortTime(&items[i], key)
		right := threadSortTime(&items[j], key)
		if left.Equal(right) {
			if direction == SortAsc {
				return items[i].ThreadID < items[j].ThreadID
			}
			return items[i].ThreadID > items[j].ThreadID
		}
		if direction == SortAsc {
			return left.Before(right)
		}
		return left.After(right)
	})
}

func threadSortTime(item *ThreadItem, key SortKey) time.Time {
	switch key {
	case SortUpdatedAt:
		return item.UpdatedAt
	case SortRecencyAt:
		return item.RecencyAt
	default:
		return item.CreatedAt
	}
}

func parseCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(cursor)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid cursor %q", cursor)
	}
	return value, nil
}

func userTextFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var item Item
	if err := json.Unmarshal(raw, &item); err != nil {
		return ""
	}
	if item.Type != "user_message" && !(item.Type == "message" && item.Role == "user") {
		return ""
	}
	if strings.TrimSpace(item.Text) != "" {
		return item.Text
	}
	if text, ok := item.Data["text"].(string); ok {
		return text
	}
	for i := range item.Content {
		if strings.TrimSpace(item.Content[i].Text) != "" {
			return item.Content[i].Text
		}
	}
	return ""
}

func userTextFromLine(line *Line) string {
	if line == nil {
		return ""
	}
	if text := userTextFromRaw(line.Item); text != "" {
		return text
	}
	if line.Type == "event_msg" {
		if text := userTextFromEventPayload(line.Payload); text != "" {
			return text
		}
	}
	if text, ok := line.Data["text"].(string); ok {
		return text
	}
	return ""
}

func userTextFromEventPayload(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var payload struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if normalizeRolloutEventType(payload.Type) != "user_message" {
		return ""
	}
	return firstNonEmpty(payload.Message, payload.Text)
}

func lineItemTime(line Line) time.Time {
	if line.Timestamp == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, line.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func messageInputItem(item *Item) map[string]any {
	role := firstNonEmpty(item.Role, roleForItemType(item.Type), "user")
	content := contentBlocksFromItem(item, role)
	if len(content) == 0 {
		return nil
	}
	return map[string]any{
		"type":    "message",
		"role":    role,
		"content": content,
	}
}

func toolCallInputItem(item *Item) map[string]any {
	callID := firstNonEmpty(item.CallID, stringValue(item.Data, "call_id"), item.ID)
	switch item.Type {
	case "custom_tool_call":
		return map[string]any{
			"id":      item.ID,
			"type":    "custom_tool_call",
			"call_id": callID,
			"name":    firstNonEmpty(item.Name, stringValue(item.Data, "name")),
			"input":   firstNonEmpty(item.Text, stringValue(item.Data, "input")),
		}
	case "tool_search_call":
		out := map[string]any{
			"id":        item.ID,
			"type":      "tool_search_call",
			"call_id":   callID,
			"execution": firstNonEmpty(stringValue(item.Data, "execution"), "client"),
		}
		if search := mapValue(item.Data, "arguments"); search != nil {
			out["arguments"] = search
		} else if search := mapValue(item.Data, "search"); search != nil {
			out["arguments"] = search
		}
		return out
	default:
		return map[string]any{
			"id":        item.ID,
			"type":      "function_call",
			"call_id":   callID,
			"name":      firstNonEmpty(item.Name, stringValue(item.Data, "name")),
			"arguments": firstNonEmpty(item.Text, stringValue(item.Data, "arguments")),
		}
	}
}

func toolOutputInputItem(item *Item) map[string]any {
	callID := firstNonEmpty(item.CallID, stringValue(item.Data, "call_id"), item.ID)
	outputType := item.Type
	if outputType == "tool_output" {
		outputType = "function_call_output"
	}
	out := map[string]any{
		"type":    outputType,
		"call_id": callID,
	}
	if outputType == "tool_search_output" {
		out["status"] = firstNonEmpty(stringValue(item.Data, "status"), "completed")
		out["execution"] = firstNonEmpty(stringValue(item.Data, "execution"), "client")
		if tools, ok := item.Data["tools"]; ok {
			out["tools"] = tools
		}
		return out
	}
	output := any(firstNonEmpty(item.Text, stringValue(item.Data, "output")))
	if value, ok := item.Data["content_items"]; ok {
		output = value
	}
	out["output"] = output
	return out
}

func contentBlocksFromItem(item *Item, role string) []map[string]any {
	if len(item.Content) > 0 {
		blocks := make([]map[string]any, 0, len(item.Content))
		for i := range item.Content {
			block := map[string]any{"type": firstNonEmpty(item.Content[i].Type, defaultContentType(role))}
			if item.Content[i].Text != "" {
				block["text"] = item.Content[i].Text
			}
			if item.Content[i].ImageURL != "" {
				block["image_url"] = item.Content[i].ImageURL
			}
			if item.Content[i].Detail != nil {
				block["detail"] = *item.Content[i].Detail
			}
			blocks = append(blocks, block)
		}
		return blocks
	}
	text := firstNonEmpty(item.Text, stringValue(item.Data, "text"))
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []map[string]any{{"type": defaultContentType(role), "text": text}}
}

func roleForItemType(itemType string) string {
	switch itemType {
	case "agent_message", "assistant_message":
		return "assistant"
	case "user_message", "message":
		return "user"
	default:
		return ""
	}
}

func defaultContentType(role string) string {
	if role == "assistant" {
		return "output_text"
	}
	return "input_text"
}

func stringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func mapValue(values map[string]any, key string) map[string]any {
	if values == nil {
		return nil
	}
	value, _ := values[key].(map[string]any)
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneRawMessages(values []json.RawMessage) []json.RawMessage {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		if len(value) == 0 {
			continue
		}
		cloned = append(cloned, append(json.RawMessage(nil), value...))
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
