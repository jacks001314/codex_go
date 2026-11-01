package rollout

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"codex_go/session"
	"codex_go/utils"
)

// This file ports the Rust thread-store legacy rollout canonicalizer
// (codex-rs/thread-store/src/local/rollout_migration/canonicalizer.rs). The
// canonicalizer is a record replay, not a session-item projection: legacy
// response items are re-emitted verbatim, obsolete completion events are
// converted into canonical turn items, and every surviving record carries the
// next stable ordinal. Turn ids for records that predate explicit turn
// boundaries are synthesized as "rollout-{source_index}", matching Rust's
// ensure_turn/start_implicit_turn.

// canonicalJSONObject writes a JSON object in an explicit field order so the
// staged rollout keeps Rust's serde field order instead of Go's map ordering.
type canonicalJSONObject struct {
	buf   bytes.Buffer
	first bool
}

func newCanonicalJSONObject() *canonicalJSONObject {
	return &canonicalJSONObject{first: true}
}

func (o *canonicalJSONObject) field(name string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal canonical field %s: %w", name, err)
	}
	return o.rawField(name, encoded)
}

func (o *canonicalJSONObject) rawField(name string, value []byte) error {
	if len(value) == 0 {
		value = []byte("null")
	}
	key, err := json.Marshal(name)
	if err != nil {
		return fmt.Errorf("marshal canonical field name %s: %w", name, err)
	}
	if o.first {
		o.buf.WriteByte('{')
		o.first = false
	} else {
		o.buf.WriteByte(',')
	}
	o.buf.Write(key)
	o.buf.WriteByte(':')
	o.buf.Write(value)
	return nil
}

func (o *canonicalJSONObject) finish() []byte {
	if o.first {
		return []byte("{}")
	}
	o.buf.WriteByte('}')
	return o.buf.Bytes()
}

// canonicalReplayLine is one surviving legacy rollout record after parsing and
// rollback planning.
type canonicalReplayLine struct {
	// sourceIndex is the 0-based JSONL line index, matching Rust's
	// source_line_index that seeds synthetic turn ids.
	sourceIndex uint64
	timestamp   string
	lineType    string
	payload     json.RawMessage
	metadata    json.RawMessage
	eventType   string
	// boundary groups records into logical instruction turns so a later
	// thread_rolled_back marker can drop whole turns.
	boundary int
	// rollback is the num_turns of a thread_rolled_back marker.
	rollback int
}

type canonicalRawLine struct {
	Type        string          `json:"type"`
	Timestamp   string          `json:"timestamp"`
	Payload     json.RawMessage `json:"payload"`
	Item        json.RawMessage `json:"item"`
	Metadata    json.RawMessage `json:"metadata"`
	TurnContext json.RawMessage `json:"turn_context"`
}

// readCanonicalReplayLines reads the rollout's raw JSONL records, decompressing
// .jsonl.zst sources, and reports the decompressed byte count.
func readCanonicalReplayLines(path string) ([][]byte, uint64, error) {
	reader, err := OpenRolloutLineReader(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = reader.Close() }()
	var (
		lines        [][]byte
		decompressed uint64
	)
	for {
		raw, readErr := reader.ReadLine()
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, 0, fmt.Errorf("read legacy rollout: %w", readErr)
		}
		// Rust's migration rate limiter charges the decompressed stream exactly
		// as it sits on disk, so count the bytes ReadLine consumed before
		// trimming the delimiter: the trailing newline is part of the source.
		decompressed += uint64(len(raw))
		raw = bytes.TrimRight(raw, "\r\n")
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		lines = append(lines, append([]byte(nil), raw...))
	}
	return lines, decompressed, nil
}

// planCanonicalReplay converts raw legacy records into the records the writer
// replays, assigning turn boundaries and applying rollback markers.
func planCanonicalReplay(rawLines [][]byte) ([]canonicalReplayLine, int, error) {
	planned := make([]canonicalReplayLine, 0, len(rawLines))
	firstMarker := -1
	boundary := 0
	explicitOpen := false
	sawUser := false
	pendingStart := -1
	for index, raw := range rawLines {
		var decoded canonicalRawLine
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, -1, fmt.Errorf("parse legacy rollout line %d: %w", index+1, err)
		}
		lineType := strings.TrimSpace(decoded.Type)
		if lineType == "" {
			return nil, -1, fmt.Errorf("legacy rollout line %d has no type", index+1)
		}
		if lineType == "session_meta" {
			continue
		}
		entry := canonicalReplayLine{
			sourceIndex: uint64(index),
			timestamp:   decoded.Timestamp,
			lineType:    lineType,
		}
		switch lineType {
		case "response_item":
			entry.payload = firstNonEmptyRaw(decoded.Item, decoded.Payload)
			entry.metadata = decoded.Metadata
		case "turn_context":
			entry.payload = firstNonEmptyRaw(decoded.TurnContext, decoded.Payload)
		default:
			entry.payload = decoded.Payload
		}
		if lineType == "event_msg" {
			entry.eventType = canonicalRawString(entry.payload, "type")
			switch entry.eventType {
			case "thread_rolled_back":
				entry.rollback = canonicalRawInt(entry.payload, "num_turns")
				if firstMarker < 0 {
					firstMarker = len(planned)
				}
				entry.boundary = boundary
				planned = append(planned, entry)
				continue
			case "task_started":
				explicitOpen = true
				sawUser = false
				pendingStart = len(planned)
			case "turn_aborted", "task_complete":
				explicitOpen = false
				sawUser = false
				pendingStart = -1
			case "user_message":
				if explicitOpen && !sawUser {
					// The turn's first message adopts the turn-started records
					// that introduced it, so rolling back this instruction also
					// removes the turn boundary Rust drops.
					boundary++
					if pendingStart >= 0 {
						for i := pendingStart; i < len(planned); i++ {
							planned[i].boundary = boundary
						}
					}
					pendingStart = -1
				} else {
					boundary++
				}
				sawUser = true
			}
		}
		entry.boundary = boundary
		planned = append(planned, entry)
	}
	return planned, firstMarker, nil
}

func firstNonEmptyRaw(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(bytes.TrimSpace(value)) > 0 {
			return value
		}
	}
	return nil
}

func canonicalRawString(raw json.RawMessage, key string) string {
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return ""
	}
	value, ok := decoded[key]
	if !ok {
		return ""
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return ""
	}
	return text
}

func canonicalRawInt(raw json.RawMessage, key string) int {
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return 0
	}
	value, ok := decoded[key]
	if !ok {
		return 0
	}
	var number int
	if err := json.Unmarshal(value, &number); err != nil {
		return 0
	}
	return number
}

// applyCanonicalRollbackPlan drops the records of rolled-back instruction
// turns. Legacy rollback removes logical turns rather than a physical suffix,
// so the plan is applied before ordinals are assigned.
func applyCanonicalRollbackPlan(planned []canonicalReplayLine) []canonicalReplayLine {
	kept := make([]canonicalReplayLine, 0, len(planned))
	for i := range planned {
		entry := planned[i]
		if entry.eventType == "thread_rolled_back" {
			kept = dropCanonicalReplayTurns(kept, entry.rollback)
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func dropCanonicalReplayTurns(kept []canonicalReplayLine, numTurns int) []canonicalReplayLine {
	if numTurns <= 0 || len(kept) == 0 {
		return kept
	}
	boundaries := make([]int, 0, 8)
	seen := make(map[int]bool, 8)
	for i := range kept {
		value := kept[i].boundary
		if value <= 0 || seen[value] {
			continue
		}
		seen[value] = true
		boundaries = append(boundaries, value)
	}
	if len(boundaries) == 0 {
		return kept
	}
	sort.Ints(boundaries)
	if numTurns < len(boundaries) {
		boundaries = boundaries[len(boundaries)-numTurns:]
	}
	drop := make(map[int]bool, len(boundaries))
	for _, value := range boundaries {
		drop[value] = true
	}
	out := kept[:0]
	for i := range kept {
		if drop[kept[i].boundary] {
			continue
		}
		out = append(out, kept[i])
	}
	return out
}

// canonicalReasoningState accumulates the legacy agent_reasoning events into
// one Reasoning turn item, mirroring Rust's write_reasoning.
type canonicalReasoningState struct {
	id         string
	summary    []string
	rawContent []string
}

// canonicalReplayWriter replays planned records into the staged rollout.
type canonicalReplayWriter struct {
	out            io.Writer
	threadID       string
	nextOrdinal    uint64
	nextItemIndex  uint64
	bytesWritten   uint64
	activeTurnID   string
	activeExplicit bool
	activeSawUser  bool
	knownTurnIDs   map[string]bool
	reasoning      *canonicalReasoningState
}

func newCanonicalReplayWriter(out io.Writer, threadID string) *canonicalReplayWriter {
	return &canonicalReplayWriter{
		out:           out,
		threadID:      threadID,
		nextOrdinal:   0,
		nextItemIndex: 1,
		knownTurnIDs:  map[string]bool{},
	}
}

func (w *canonicalReplayWriter) itemID() string {
	id := fmt.Sprintf("item-%d", w.nextItemIndex)
	w.nextItemIndex++
	return id
}

// writeRecord appends one canonical JSONL record with Rust's RolloutLine field
// order: timestamp, ordinal, type, payload.
func (w *canonicalReplayWriter) writeRecord(timestamp string, lineType string, payload []byte) error {
	return w.writeRecordWithMetadata(timestamp, lineType, payload, nil)
}

func (w *canonicalReplayWriter) writeRecordWithMetadata(timestamp string, lineType string, payload []byte, metadata json.RawMessage) error {
	record := newCanonicalJSONObject()
	if err := record.field("timestamp", timestamp); err != nil {
		return err
	}
	if err := record.field("ordinal", w.nextOrdinal); err != nil {
		return err
	}
	if err := record.field("type", lineType); err != nil {
		return err
	}
	if err := record.rawField("payload", payload); err != nil {
		return err
	}
	if len(bytes.TrimSpace(metadata)) > 0 && string(bytes.TrimSpace(metadata)) != "null" {
		if err := record.rawField("metadata", metadata); err != nil {
			return err
		}
	}
	data := append(record.finish(), '\n')
	if _, err := w.out.Write(data); err != nil {
		return fmt.Errorf("write canonical %s record: %w", lineType, err)
	}
	w.nextOrdinal++
	w.bytesWritten += uint64(len(data))
	return nil
}

// writeHeadSessionMeta writes the canonical session_meta header: history_mode
// becomes paginated and the inherited history base is dropped, matching Rust's
// write_head_session_meta.
func (w *canonicalReplayWriter) writeHeadSessionMeta(timestamp string, meta *SessionMeta) error {
	payload, err := marshalCanonicalSessionMeta(meta)
	if err != nil {
		return err
	}
	return w.writeRecord(timestamp, "session_meta", payload)
}

// marshalCanonicalSessionMeta reproduces Rust's SessionMeta serde output:
// fields keep declaration order, optional fields are skipped when unset, and
// model_provider/base_instructions always serialize (as null when absent).
func marshalCanonicalSessionMeta(meta *SessionMeta) ([]byte, error) {
	out := newCanonicalJSONObject()
	if meta == nil {
		return out.finish(), nil
	}
	if meta.CreatorUserID != "" {
		if err := out.field("creator_user_id", meta.CreatorUserID); err != nil {
			return nil, err
		}
	}
	if meta.CreatorAccountID != "" {
		if err := out.field("creator_account_id", meta.CreatorAccountID); err != nil {
			return nil, err
		}
	}
	threadID := strings.TrimSpace(meta.ID)
	if err := out.field("session_id", threadID); err != nil {
		return nil, err
	}
	if err := out.field("id", threadID); err != nil {
		return nil, err
	}
	if meta.ForkedFromID != "" {
		if err := out.field("forked_from_id", meta.ForkedFromID); err != nil {
			return nil, err
		}
	}
	if meta.AttachedThread {
		if err := out.field("attached_thread", true); err != nil {
			return nil, err
		}
	}
	if meta.ForkedFromOrdinalExclusive != nil {
		if err := out.field("forked_from_ordinal_exclusive", *meta.ForkedFromOrdinalExclusive); err != nil {
			return nil, err
		}
	}
	if meta.ParentThreadID != "" {
		if err := out.field("parent_thread_id", meta.ParentThreadID); err != nil {
			return nil, err
		}
	}
	if err := out.field("timestamp", meta.Timestamp); err != nil {
		return nil, err
	}
	if err := out.field("cwd", meta.CWD); err != nil {
		return nil, err
	}
	if meta.RuntimeWorkspaceRoots != nil {
		roots := *meta.RuntimeWorkspaceRoots
		if roots == nil {
			roots = []string{}
		}
		if err := out.field("runtime_workspace_roots", roots); err != nil {
			return nil, err
		}
	}
	if err := out.field("originator", meta.Originator); err != nil {
		return nil, err
	}
	if err := out.field("cli_version", meta.CLIVersion); err != nil {
		return nil, err
	}
	if err := out.field("source", canonicalSessionSource(meta.Source)); err != nil {
		return nil, err
	}
	if meta.ThreadSource != "" {
		if err := out.field("thread_source", meta.ThreadSource); err != nil {
			return nil, err
		}
	}
	if meta.AgentNickname != "" {
		if err := out.field("agent_nickname", meta.AgentNickname); err != nil {
			return nil, err
		}
	}
	if meta.AgentRole != "" {
		if err := out.field("agent_role", meta.AgentRole); err != nil {
			return nil, err
		}
	}
	if meta.AgentPath != "" {
		if err := out.field("agent_path", meta.AgentPath); err != nil {
			return nil, err
		}
	}
	if meta.ModelProvider != "" {
		if err := out.field("model_provider", meta.ModelProvider); err != nil {
			return nil, err
		}
	} else if err := out.rawField("model_provider", []byte("null")); err != nil {
		return nil, err
	}
	if meta.BaseInstructions != "" {
		base := newCanonicalJSONObject()
		if err := base.field("text", meta.BaseInstructions); err != nil {
			return nil, err
		}
		if err := out.rawField("base_instructions", base.finish()); err != nil {
			return nil, err
		}
	} else if err := out.rawField("base_instructions", []byte("null")); err != nil {
		return nil, err
	}
	if len(meta.DynamicTools) > 0 {
		if err := out.field("dynamic_tools", meta.DynamicTools); err != nil {
			return nil, err
		}
	}
	if len(meta.SelectedCapabilityRoots) > 0 {
		if err := out.field("selected_capability_roots", meta.SelectedCapabilityRoots); err != nil {
			return nil, err
		}
	}
	if meta.MemoryMode != "" {
		if err := out.field("memory_mode", meta.MemoryMode); err != nil {
			return nil, err
		}
	}
	if err := out.field("history_mode", "paginated"); err != nil {
		return nil, err
	}
	if meta.MultiAgentVersion != "" {
		if err := out.field("multi_agent_version", meta.MultiAgentVersion); err != nil {
			return nil, err
		}
	}
	if len(bytes.TrimSpace(meta.ContextWindow)) > 0 {
		if err := out.rawField("context_window", meta.ContextWindow); err != nil {
			return nil, err
		}
	}
	if len(meta.Git) > 0 {
		if err := out.field("git", meta.Git); err != nil {
			return nil, err
		}
	}
	return out.finish(), nil
}

func canonicalSessionSource(source string) any {
	if subAgent, ok := session.SubAgentSourceWire(source); ok {
		return map[string]any{"subagent": subAgent}
	}
	if strings.TrimSpace(source) == "" {
		return "cli"
	}
	return source
}

// canonicalTimestampSeconds parses one rollout timestamp into unix seconds.
func canonicalTimestampSeconds(timestamp string) (int64, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(timestamp))
	if err != nil {
		return 0, fmt.Errorf("parse rollout timestamp %q: %w", timestamp, err)
	}
	return parsed.Unix(), nil
}

func canonicalTimestampMillis(timestamp string) (int64, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(timestamp))
	if err != nil {
		return 0, fmt.Errorf("parse rollout timestamp %q: %w", timestamp, err)
	}
	return parsed.UnixMilli(), nil
}

// startImplicitTurn synthesizes the turn a legacy record belongs to: Rust's
// canonicalizer names it "rollout-{source_index}" and stamps started_at from
// the record's own timestamp.
func (w *canonicalReplayWriter) startImplicitTurn(timestamp string, turnID string) error {
	startedAt, err := canonicalTimestampSeconds(timestamp)
	if err != nil {
		return err
	}
	payload := newCanonicalJSONObject()
	if err := payload.field("type", "task_started"); err != nil {
		return err
	}
	if err := payload.field("turn_id", turnID); err != nil {
		return err
	}
	if err := payload.field("started_at", startedAt); err != nil {
		return err
	}
	if err := payload.rawField("model_context_window", []byte("null")); err != nil {
		return err
	}
	if err := payload.field("collaboration_mode_kind", "default"); err != nil {
		return err
	}
	if err := w.writeRecord(timestamp, "event_msg", payload.finish()); err != nil {
		return err
	}
	w.activeTurnID = turnID
	w.activeExplicit = false
	w.activeSawUser = false
	w.knownTurnIDs[turnID] = true
	w.reasoning = nil
	return nil
}

func (w *canonicalReplayWriter) ensureTurn(timestamp string, sourceIndex uint64) (string, error) {
	if w.activeTurnID != "" {
		return w.activeTurnID, nil
	}
	turnID := fmt.Sprintf("rollout-%d", sourceIndex)
	if err := w.startImplicitTurn(timestamp, turnID); err != nil {
		return "", err
	}
	return turnID, nil
}

func (w *canonicalReplayWriter) finishImplicitTurn(timestamp string) error {
	if w.activeTurnID == "" || w.activeExplicit {
		return nil
	}
	turnID := w.activeTurnID
	w.activeTurnID = ""
	w.activeSawUser = false
	w.reasoning = nil
	completedAt, err := canonicalTimestampSeconds(timestamp)
	if err != nil {
		return err
	}
	payload := newCanonicalJSONObject()
	if err := payload.field("type", "task_complete"); err != nil {
		return err
	}
	if err := payload.field("turn_id", turnID); err != nil {
		return err
	}
	if err := payload.rawField("last_agent_message", []byte("null")); err != nil {
		return err
	}
	if err := payload.field("completed_at", completedAt); err != nil {
		return err
	}
	return w.writeRecord(timestamp, "event_msg", payload.finish())
}

// writeItemCompleted wraps a canonical turn item in an ItemCompleted event.
func (w *canonicalReplayWriter) writeItemCompleted(timestamp string, turnID string, item []byte) error {
	completedAtMS, err := canonicalTimestampMillis(timestamp)
	if err != nil {
		return err
	}
	payload := newCanonicalJSONObject()
	if err := payload.field("type", "item_completed"); err != nil {
		return err
	}
	if err := payload.field("thread_id", w.threadID); err != nil {
		return err
	}
	if err := payload.field("turn_id", turnID); err != nil {
		return err
	}
	if err := payload.rawField("item", item); err != nil {
		return err
	}
	if err := payload.field("completed_at_ms", completedAtMS); err != nil {
		return err
	}
	return w.writeRecord(timestamp, "event_msg", payload.finish())
}

// replay writes one planned record.
func (w *canonicalReplayWriter) replay(entry canonicalReplayLine) error {
	if entry.lineType == "response_item" {
		handled, err := w.replayLegacyExtensionItem(entry)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
		return w.writeRecordWithMetadata(entry.timestamp, entry.lineType, entry.payload, entry.metadata)
	}
	if entry.lineType != "event_msg" {
		payload := entry.payload
		if entry.lineType == "turn_context" {
			normalized, ok, err := canonicalTurnContextPayload(entry.payload)
			if err != nil {
				return err
			}
			if !ok {
				// Rust decodes the record into TurnContextItem first and drops
				// the whole line when that fails.
				return nil
			}
			payload = normalized
		}
		if entry.lineType == "compacted" {
			normalized, ok, err := canonicalCompactedPayload(entry.payload)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
			payload = normalized
		}
		return w.writeRecordWithMetadata(entry.timestamp, entry.lineType, payload, entry.metadata)
	}
	return w.replayEvent(entry)
}

// canonicalTurnContextPayload re-serializes a legacy turn_context record the way
// 0.166's TurnContextItem does: the struct's own field order, optional fields
// dropped when unset, and legacy sandbox/approval spellings normalized. A record
// the modern type cannot decode is dropped like Rust's failed parse.
func canonicalTurnContextPayload(source json.RawMessage) ([]byte, bool, error) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false, err
	}
	cwd, ok := canonicalRawJSONString(fields["cwd"])
	if !ok {
		return nil, false, nil
	}
	model, ok := canonicalRawJSONString(fields["model"])
	if !ok {
		return nil, false, nil
	}
	approval, ok, err := canonicalApprovalPolicy(fields["approval_policy"])
	if err != nil || !ok {
		return nil, false, err
	}
	sandbox, ok, err := canonicalSandboxPolicy(fields["sandbox_policy"])
	if err != nil || !ok {
		return nil, false, err
	}
	computed := map[string][]byte{
		"cwd":             cwd,
		"approval_policy": approval,
		"sandbox_policy":  sandbox,
		"model":           model,
	}
	out := newCanonicalJSONObject()
	for _, key := range []string{
		"turn_id",
		"root_turn_id",
		"disabled_plugin_ids",
		"cwd",
		"approval_policy",
		"approvals_reviewer",
		"sandbox_policy",
		"permission_profile",
		"active_permission_profile",
		"file_system_sandbox_policy",
		"model",
		"comp_hash",
		"collaboration_mode",
		"multi_agent_version",
		"realtime_active",
		"cyber_access_program",
		"effort",
		"summary",
	} {
		if value, ok := computed[key]; ok {
			if err := out.rawField(key, value); err != nil {
				return nil, false, err
			}
			continue
		}
		value, present := fields[key]
		if !present || canonicalRawIsNull(value) {
			continue
		}
		if key == "collaboration_mode" {
			normalized, ok, err := canonicalCollaborationMode(value)
			if err != nil || !ok {
				return nil, false, err
			}
			value = normalized
		}
		if err := out.rawField(key, value); err != nil {
			return nil, false, err
		}
	}
	return out.finish(), true, nil
}

// canonicalRawJSONString returns the raw JSON for a string field.
func canonicalRawJSONString(value json.RawMessage) ([]byte, bool) {
	text, ok := canonicalRawJSONStringValue(value)
	if !ok {
		return nil, false
	}
	return []byte(marshalCanonicalString(text)), true
}

// marshalCanonicalString encodes a string the way serde_json does.
func marshalCanonicalString(text string) string {
	encoded, err := json.Marshal(text)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// canonicalApprovalPolicy mirrors AskForApproval's decoding: "on-failure" is an
// alias of "on-request", granular approvals keep their tagged object, and any
// other spelling fails the parse.
func canonicalApprovalPolicy(value json.RawMessage) ([]byte, bool, error) {
	if len(value) == 0 || canonicalRawIsNull(value) {
		return nil, false, nil
	}
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		switch text {
		case "untrusted", "on-request", "never":
			return []byte(marshalCanonicalString(text)), true, nil
		case "on-failure":
			return []byte(`"on-request"`), true, nil
		default:
			return nil, false, nil
		}
	}
	var tagged map[string]json.RawMessage
	if err := json.Unmarshal(value, &tagged); err != nil {
		return nil, false, nil
	}
	if _, ok := tagged["granular"]; !ok {
		return nil, false, nil
	}
	return value, true, nil
}

// canonicalSandboxPolicy mirrors SandboxPolicy's decoding and serialization:
// the legacy "mode" key becomes "type", unknown fields disappear, and each
// variant emits exactly the fields the modern type always writes.
func canonicalSandboxPolicy(value json.RawMessage) ([]byte, bool, error) {
	if len(value) == 0 || canonicalRawIsNull(value) {
		return nil, false, nil
	}
	fields, err := canonicalRawFields(value)
	if err != nil {
		return nil, false, err
	}
	policyType := ""
	if raw, ok := fields["type"]; ok {
		if policyType, ok = canonicalRawJSONStringValue(raw); !ok {
			return nil, false, nil
		}
	} else if raw, ok := fields["mode"]; ok {
		if policyType, ok = canonicalRawJSONStringValue(raw); !ok {
			return nil, false, nil
		}
	}
	out := newCanonicalJSONObject()
	switch policyType {
	case "danger-full-access":
		_ = out.field("type", policyType)
	case "read-only":
		_ = out.field("type", policyType)
		if raw, ok := fields["network_access"]; ok {
			enabled, ok := canonicalRawJSONBool(raw)
			if !ok {
				return nil, false, nil
			}
			if enabled {
				_ = out.rawField("network_access", []byte("true"))
			}
		}
	case "external-sandbox":
		_ = out.field("type", policyType)
		access := "restricted"
		if raw, ok := fields["network_access"]; ok {
			name, ok := canonicalRawJSONStringValue(raw)
			if !ok || (name != "restricted" && name != "enabled") {
				return nil, false, nil
			}
			access = name
		}
		_ = out.field("network_access", access)
	case "workspace-write":
		_ = out.field("type", policyType)
		if raw, ok := fields["writable_roots"]; ok && !canonicalRawIsNull(raw) {
			var roots []string
			if err := json.Unmarshal(raw, &roots); err != nil {
				return nil, false, nil
			}
			if len(roots) > 0 {
				_ = out.rawField("writable_roots", raw)
			}
		}
		for _, key := range []string{"network_access", "exclude_tmpdir_env_var", "exclude_slash_tmp"} {
			enabled := false
			if raw, ok := fields[key]; ok {
				var okBool bool
				if enabled, okBool = canonicalRawJSONBool(raw); !okBool {
					return nil, false, nil
				}
			}
			_ = out.field(key, enabled)
		}
	default:
		return nil, false, nil
	}
	return out.finish(), true, nil
}

func canonicalRawJSONStringValue(value json.RawMessage) (string, bool) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", false
	}
	return text, true
}

func canonicalRawJSONBool(value json.RawMessage) (bool, bool) {
	var flag bool
	if err := json.Unmarshal(value, &flag); err != nil {
		return false, false
	}
	return flag, true
}

// canonicalCollaborationMode mirrors CollaborationMode's field order and the
// legacy settings migration line_parser performs before decoding.
func canonicalCollaborationMode(value json.RawMessage) ([]byte, bool, error) {
	fields, err := canonicalRawFields(value)
	if err != nil {
		return nil, false, err
	}
	mode, ok := canonicalRawJSONStringValue(fields["mode"])
	if !ok || (mode != "default" && mode != "plan") {
		return nil, false, nil
	}
	settingsRaw, ok := fields["settings"]
	if !ok {
		legacy := map[string]json.RawMessage{}
		for _, key := range []string{"model", "reasoning_effort", "developer_instructions"} {
			if raw, present := fields[key]; present {
				legacy[key] = raw
			}
		}
		if len(legacy) == 0 {
			return nil, false, nil
		}
		combined, err := json.Marshal(legacy)
		if err != nil {
			return nil, false, err
		}
		settingsRaw = combined
	}
	settingsFields, err := canonicalRawFields(settingsRaw)
	if err != nil {
		return nil, false, err
	}
	if _, ok := canonicalRawJSONStringValue(settingsFields["model"]); !ok {
		return nil, false, nil
	}
	// Settings keeps its optional fields in the output: 0.166 serializes both
	// as null when unset.
	outSettings := newCanonicalJSONObject()
	model, _ := canonicalRawJSONStringValue(settingsFields["model"])
	if err := outSettings.field("model", model); err != nil {
		return nil, false, err
	}
	for _, key := range []string{"reasoning_effort", "developer_instructions"} {
		raw, present := settingsFields[key]
		if !present || canonicalRawIsNull(raw) {
			raw = []byte("null")
		}
		if err := outSettings.rawField(key, raw); err != nil {
			return nil, false, err
		}
	}
	out := newCanonicalJSONObject()
	if err := out.field("mode", mode); err != nil {
		return nil, false, err
	}
	if err := out.rawField("settings", outSettings.finish()); err != nil {
		return nil, false, err
	}
	return out.finish(), true, nil
}

// canonicalCompactedPayload re-serializes a compacted record with
// CompactedItemWire's field order; the two trailing nullable fields always
// serialize, which is what 0.166 writes.
func canonicalCompactedPayload(source json.RawMessage) ([]byte, bool, error) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false, err
	}
	message, ok := canonicalRawJSONStringValue(fields["message"])
	if !ok {
		return nil, false, nil
	}
	out := newCanonicalJSONObject()
	if err := out.field("message", message); err != nil {
		return nil, false, err
	}
	for _, key := range []string{
		"replacement_history",
		"replacement_history_metadata",
		"guardian_history",
		"retained_context",
		"mcp_resource_origins",
		"window_number",
		"first_window_id",
		"previous_window_id",
		"window_id",
	} {
		if raw, present := fields[key]; present && !canonicalRawIsNull(raw) {
			if err := out.rawField(key, raw); err != nil {
				return nil, false, err
			}
		}
	}
	for _, key := range []string{"compaction_response_id", "latest_token_usage_record"} {
		raw, present := fields[key]
		if !present || canonicalRawIsNull(raw) {
			raw = []byte("null")
		}
		if err := out.rawField(key, raw); err != nil {
			return nil, false, err
		}
	}
	if raw, present := fields["resume_metadata"]; present && !canonicalRawIsNull(raw) {
		if err := out.rawField("resume_metadata", raw); err != nil {
			return nil, false, err
		}
	}
	return out.finish(), true, nil
}

// canonicalResponseItemTypes are the response-item payloads a legacy rollout
// persisted verbatim. Go-written rollouts also stored canonical turn items
// (image generation, command execution, tool calls, ...) inside response_item
// records; those have to become ItemCompleted events or the migrated thread
// loses them on resume.
var canonicalResponseItemTypes = map[string]bool{
	"message":                 true,
	"reasoning":               true,
	"function_call":           true,
	"function_call_output":    true,
	"custom_tool_call":        true,
	"custom_tool_call_output": true,
	"tool_search_call":        true,
	"tool_search_output":      true,
	"tool_output":             true,
	"local_shell_call":        true,
	"local_shell_call_output": true,
	"web_search_call":         true,
	"computer_call":           true,
	"computer_call_output":    true,
	"image_generation_call":   true,
	"ghost_snapshot":          true,
	"audio":                   true,
}

func (w *canonicalReplayWriter) replayLegacyExtensionItem(entry canonicalReplayLine) (bool, error) {
	rawType := strings.TrimSpace(canonicalRawString(entry.payload, "type"))
	if rawType == "" || canonicalResponseItemTypes[rawType] {
		return false, nil
	}
	line := Line{Type: "item", Item: entry.payload}
	item, ok := ItemFromLine(&line)
	if !ok {
		return false, nil
	}
	createdAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.timestamp))
	if err != nil {
		return false, nil
	}
	sessionItem := SessionItemFromRolloutItem(item, createdAt, int(entry.sourceIndex))
	if strings.TrimSpace(sessionItem.ID) == "" {
		return false, nil
	}
	turnID, err := w.ensureTurn(entry.timestamp, entry.sourceIndex)
	if err != nil {
		return false, err
	}
	if sessionItem.Metadata == nil {
		sessionItem.Metadata = map[string]any{}
	}
	sessionItem.Metadata["turnId"] = turnID
	core, _, coreErr := CoreTurnItemJSONFromSessionItem(&sessionItem)
	if coreErr != nil || len(core) == 0 {
		return false, nil
	}
	return true, w.writeItemCompleted(entry.timestamp, turnID, core)
}

func (w *canonicalReplayWriter) replayEvent(entry canonicalReplayLine) error {
	switch entry.eventType {
	case "task_started":
		if err := w.finishImplicitTurn(entry.timestamp); err != nil {
			return err
		}
		turnID := canonicalRawString(entry.payload, "turn_id")
		w.activeTurnID = turnID
		w.activeExplicit = true
		w.activeSawUser = false
		w.reasoning = nil
		if turnID != "" {
			w.knownTurnIDs[turnID] = true
		}
		payload, err := canonicalTaskStartedPayload(entry.payload)
		if err != nil {
			return err
		}
		return w.writeRecord(entry.timestamp, "event_msg", payload)
	case "task_complete":
		turnID := canonicalRawString(entry.payload, "turn_id")
		w.reasoning = nil
		if w.activeTurnID == turnID {
			w.activeTurnID = ""
			w.activeSawUser = false
		}
		payload, err := canonicalTaskCompletePayload(entry.payload)
		if err != nil {
			return err
		}
		return w.writeRecord(entry.timestamp, "event_msg", payload)
	case "turn_aborted":
		turnID := canonicalRawString(entry.payload, "turn_id")
		if turnID == "" && w.activeTurnID != "" {
			turnID = w.activeTurnID
		}
		if w.activeTurnID != "" && turnID == w.activeTurnID {
			w.activeTurnID = ""
			w.activeSawUser = false
		}
		w.reasoning = nil
		payload, err := canonicalTurnAbortedPayload(entry.payload, turnID)
		if err != nil {
			return err
		}
		return w.writeRecord(entry.timestamp, "event_msg", payload)
	case "token_count":
		payload, err := canonicalTokenCountPayload(entry.payload)
		if err != nil {
			return err
		}
		return w.writeRecord(entry.timestamp, "event_msg", payload)
	case "user_message":
		if w.activeTurnID != "" && !w.activeExplicit && w.activeSawUser {
			if err := w.finishImplicitTurn(entry.timestamp); err != nil {
				return err
			}
		}
		turnID, err := w.ensureTurn(entry.timestamp, entry.sourceIndex)
		if err != nil {
			return err
		}
		w.activeSawUser = true
		w.reasoning = nil
		item := canonicalUserMessageItem(entry.payload, w.itemID())
		return w.writeItemCompleted(entry.timestamp, turnID, item)
	case "agent_message":
		if canonicalRawString(entry.payload, "message") == "" {
			// Rust converts empty agent messages to no item and drops the event.
			return nil
		}
		turnID, err := w.ensureTurn(entry.timestamp, entry.sourceIndex)
		if err != nil {
			return err
		}
		w.reasoning = nil
		item, buildErr := canonicalAgentMessageItem(entry.payload, w.itemID())
		if buildErr != nil {
			return buildErr
		}
		return w.writeItemCompleted(entry.timestamp, turnID, item)
	case "agent_reasoning", "agent_reasoning_raw_content":
		return w.writeReasoning(entry)
	case "exec_command_end":
		return w.writeCommandExecution(entry)
	case "patch_apply_end":
		return w.writePatchApplyEnd(entry)
	case "mcp_tool_call_end":
		return w.writeMcpToolCall(entry)
	case "web_search_end":
		return w.writeWebSearch(entry)
	case "context_compacted":
		return w.writeContextCompaction(entry)
	case "entered_review_mode":
		return w.writeReviewMode(entry, true)
	case "exited_review_mode":
		return w.writeReviewMode(entry, false)
	case "sub_agent_activity":
		return w.writeSubAgentActivity(entry)
	case "dynamic_tool_call_response":
		return w.writeDynamicToolCall(entry)
	case "image_generation_end":
		return w.writeImageGeneration(entry)
	case "item_completed":
		if !canonicalTurnItemDecodes(entry.payload) {
			return nil
		}
		return w.writeRecord(entry.timestamp, "event_msg", entry.payload)
	default:
		if !legacyEventPassthrough[entry.eventType] {
			// Rust decodes every legacy record and then applies its paginated
			// persistence policy, which drops transient and retired events
			// (deltas, begin events, plan updates, errors, warnings, ...).
			return nil
		}
		return w.writeRecord(entry.timestamp, "event_msg", entry.payload)
	}
}

// legacyEventPassthrough lists the legacy event payloads the canonicalizer
// still carries over verbatim: either the paginated policy keeps them as raw
// events or they are converted to completed items by a step Go has not ported
// yet. Everything else a legacy rollout can contain is dropped by Rust.
var legacyEventPassthrough = map[string]bool{
	"thread_goal_updated":     true,
	"thread_rolled_back":      true,
	"thread_settings_applied": true,
}

// legacyEventTurnID picks the turn a converted legacy event belongs to,
// mirroring canonicalizer.rs: an explicit id keeps that turn, a first-time
// explicit id opens an implicit turn named after it, and a missing id opens the
// rollout-{line} turn. Every path drops buffered reasoning first.
func (w *canonicalReplayWriter) legacyEventTurnID(entry canonicalReplayLine) (string, error) {
	w.reasoning = nil
	turnID := canonicalRawString(entry.payload, "turn_id")
	if turnID == "" {
		return w.ensureTurn(entry.timestamp, entry.sourceIndex)
	}
	if w.activeTurnID != "" || w.knownTurnIDs[turnID] {
		return turnID, nil
	}
	if err := w.startImplicitTurn(entry.timestamp, turnID); err != nil {
		return "", err
	}
	return turnID, nil
}

// canonicalStrictString decodes a required JSON string and rejects null or
// non-string values, matching serde's String deserializer.
func canonicalStrictString(value json.RawMessage) (string, bool) {
	if len(value) == 0 || canonicalRawIsNull(value) {
		return "", false
	}
	return canonicalRawJSONStringValue(value)
}

// canonicalStrictInt decodes a required JSON integer, matching serde's integer
// deserializers that reject fractional numbers.
func canonicalStrictInt(value json.RawMessage) (int64, bool) {
	if len(value) == 0 || canonicalRawIsNull(value) {
		return 0, false
	}
	var number int64
	if err := json.Unmarshal(value, &number); err != nil {
		return 0, false
	}
	return number, true
}

// canonicalDuration replays serde's std::time::Duration shape, whose visitor
// accepts only the secs and nanos keys and rejects unknown fields.
func canonicalDuration(value json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(value)
	if err != nil {
		return nil, false
	}
	for key := range fields {
		if key != "secs" && key != "nanos" {
			return nil, false
		}
	}
	secs, ok := canonicalStrictInt(fields["secs"])
	if !ok || secs < 0 {
		return nil, false
	}
	nanos, ok := canonicalStrictInt(fields["nanos"])
	if !ok || nanos < 0 || nanos > 999999999 {
		return nil, false
	}
	out := newCanonicalJSONObject()
	_ = out.field("secs", secs)
	_ = out.field("nanos", nanos)
	return out.finish(), true
}

// canonicalCallToolResult replays CallToolResult's camelCase field order and
// drops fields the modern type does not know.
func canonicalCallToolResult(value json.RawMessage) ([]byte, bool, bool) {
	fields, err := canonicalRawFields(value)
	if err != nil {
		return nil, false, false
	}
	content, ok := fields["content"]
	if !ok || canonicalRawIsNull(content) {
		return nil, false, false
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(content, &entries); err != nil {
		return nil, false, false
	}
	out := newCanonicalJSONObject()
	_ = out.rawField("content", content)
	if raw, present := fields["structuredContent"]; present && !canonicalRawIsNull(raw) {
		_ = out.rawField("structuredContent", raw)
	}
	isError := false
	if raw, present := fields["isError"]; present && !canonicalRawIsNull(raw) {
		flag, ok := canonicalRawJSONBool(raw)
		if !ok {
			return nil, false, false
		}
		_ = out.rawField("isError", raw)
		isError = flag
	}
	if raw, present := fields["_meta"]; present && !canonicalRawIsNull(raw) {
		_ = out.rawField("_meta", raw)
	}
	return out.finish(), isError, true
}

// canonicalMcpAppUi replays McpAppUi's camelCase shape.
func canonicalMcpAppUi(value json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(value)
	if err != nil {
		return nil, false
	}
	resourceURI, ok := canonicalStrictString(fields["resourceUri"])
	if !ok {
		return nil, false
	}
	mode, ok := canonicalStrictString(fields["preferredModelDisplayMode"])
	if !ok || (mode != "inline" && mode != "fullscreen") {
		return nil, false
	}
	out := newCanonicalJSONObject()
	_ = out.field("resourceUri", resourceURI)
	_ = out.field("preferredModelDisplayMode", mode)
	return out.finish(), true
}

// canonicalWebSearchAction replays the Responses API web search action enum.
// Unknown action types decode as the Other variant in Rust, which serializes
// back as {"type":"other"}.
func canonicalWebSearchAction(value json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(value)
	if err != nil {
		return nil, false
	}
	kind, ok := canonicalStrictString(fields["type"])
	if !ok {
		kind = "other"
	}
	out := newCanonicalJSONObject()
	switch kind {
	case "search":
		_ = out.field("type", kind)
		if raw, present := fields["query"]; present && !canonicalRawIsNull(raw) {
			text, ok := canonicalRawJSONStringValue(raw)
			if !ok {
				return nil, false
			}
			_ = out.field("query", text)
		}
		if raw, present := fields["queries"]; present && !canonicalRawIsNull(raw) {
			var queries []string
			if err := json.Unmarshal(raw, &queries); err != nil {
				return nil, false
			}
			_ = out.field("queries", queries)
		}
	case "open_page", "find_in_page":
		_ = out.field("type", kind)
		for _, key := range []string{"url", "pattern"} {
			if raw, present := fields[key]; present && !canonicalRawIsNull(raw) {
				text, ok := canonicalRawJSONStringValue(raw)
				if !ok {
					return nil, false
				}
				_ = out.field(key, text)
			}
		}
	default:
		_ = out.field("type", "other")
	}
	return out.finish(), true
}

// writeWebSearch converts a legacy web_search_end event into the WebSearch
// completed item.
func (w *canonicalReplayWriter) writeWebSearch(entry canonicalReplayLine) error {
	fields, err := canonicalRawFields(entry.payload)
	if err != nil {
		return err
	}
	callID, ok := canonicalStrictString(fields["call_id"])
	if !ok {
		return nil
	}
	query, ok := canonicalStrictString(fields["query"])
	if !ok {
		return nil
	}
	action, ok := canonicalWebSearchAction(fields["action"])
	if !ok {
		return nil
	}
	out := newCanonicalJSONObject()
	_ = out.field("type", "WebSearch")
	_ = out.field("id", callID)
	_ = out.field("query", query)
	_ = out.rawField("action", action)
	if raw, present := fields["results"]; present && !canonicalRawIsNull(raw) {
		var results []json.RawMessage
		if err := json.Unmarshal(raw, &results); err != nil {
			return nil
		}
		_ = out.rawField("results", raw)
	}
	turnID, err := w.legacyEventTurnID(entry)
	if err != nil {
		return err
	}
	return w.writeItemCompleted(entry.timestamp, turnID, out.finish())
}

// writeContextCompaction converts a legacy context_compacted event into the
// ContextCompaction completed item.
func (w *canonicalReplayWriter) writeContextCompaction(entry canonicalReplayLine) error {
	out := newCanonicalJSONObject()
	_ = out.field("type", "ContextCompaction")
	_ = out.field("id", w.itemID())
	turnID, err := w.legacyEventTurnID(entry)
	if err != nil {
		return err
	}
	return w.writeItemCompleted(entry.timestamp, turnID, out.finish())
}

// writeMcpToolCall converts a legacy mcp_tool_call_end event into the
// McpToolCall completed item (legacy_event.rs completed_item).
func (w *canonicalReplayWriter) writeMcpToolCall(entry canonicalReplayLine) error {
	fields, err := canonicalRawFields(entry.payload)
	if err != nil {
		return err
	}
	callID, ok := canonicalStrictString(fields["call_id"])
	if !ok {
		return nil
	}
	invocation, err := canonicalRawFields(fields["invocation"])
	if err != nil {
		return nil
	}
	server, ok := canonicalStrictString(invocation["server"])
	if !ok {
		return nil
	}
	tool, ok := canonicalStrictString(invocation["tool"])
	if !ok {
		return nil
	}
	duration, ok := canonicalDuration(fields["duration"])
	if !ok {
		return nil
	}
	result, resultErr := canonicalRawFields(fields["result"])
	if resultErr != nil {
		return nil
	}
	status := "completed"
	var resultOut, errorOut []byte
	if payload, present := result["Ok"]; present {
		converted, isError, ok := canonicalCallToolResult(payload)
		if !ok {
			return nil
		}
		resultOut = converted
		if isError {
			status = "failed"
		}
	} else if message, present := result["Err"]; present {
		text, ok := canonicalStrictString(message)
		if !ok {
			return nil
		}
		status = "failed"
		inner := newCanonicalJSONObject()
		_ = inner.field("message", text)
		errorOut = inner.finish()
	} else {
		return nil
	}
	arguments := []byte("null")
	if raw, present := invocation["arguments"]; present && !canonicalRawIsNull(raw) {
		arguments = raw
	}
	out := newCanonicalJSONObject()
	_ = out.field("type", "McpToolCall")
	_ = out.field("id", callID)
	_ = out.field("server", server)
	_ = out.field("tool", tool)
	_ = out.rawField("arguments", arguments)
	for _, pair := range []struct{ key, name string }{
		{"connector_id", "connectorId"},
		{"mcp_app_resource_uri", "mcpAppResourceUri"},
	} {
		if raw, present := fields[pair.key]; present && !canonicalRawIsNull(raw) {
			text, ok := canonicalStrictString(raw)
			if !ok {
				return nil
			}
			_ = out.field(pair.name, text)
		}
	}
	if raw, present := fields["mcp_app_ui"]; present && !canonicalRawIsNull(raw) {
		converted, ok := canonicalMcpAppUi(raw)
		if !ok {
			return nil
		}
		_ = out.rawField("mcpAppUi", converted)
	}
	for _, pair := range []struct{ key, name string }{
		{"link_id", "linkId"},
		{"app_name", "appName"},
		{"action_name", "actionName"},
		{"plugin_id", "pluginId"},
	} {
		if raw, present := fields[pair.key]; present && !canonicalRawIsNull(raw) {
			text, ok := canonicalStrictString(raw)
			if !ok {
				return nil
			}
			_ = out.field(pair.name, text)
		}
	}
	if raw, present := fields["read_only_hint"]; present && !canonicalRawIsNull(raw) {
		flag, ok := canonicalRawJSONBool(raw)
		if !ok {
			return nil
		}
		_ = out.field("readOnlyHint", flag)
	}
	_ = out.field("status", status)
	if resultOut != nil {
		_ = out.rawField("result", resultOut)
	} else {
		_ = out.rawField("error", errorOut)
	}
	_ = out.rawField("duration", duration)
	turnID, err := w.legacyEventTurnID(entry)
	if err != nil {
		return err
	}
	return w.writeItemCompleted(entry.timestamp, turnID, out.finish())
}

// canonicalReviewTarget replays ReviewTarget's internally tagged camelCase
// enum, dropping fields the modern type does not carry.
func canonicalReviewTarget(value json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(value)
	if err != nil {
		return nil, false
	}
	kind, ok := canonicalStrictString(fields["type"])
	if !ok {
		return nil, false
	}
	out := newCanonicalJSONObject()
	_ = out.field("type", kind)
	switch kind {
	case "uncommittedChanges":
	case "baseBranch":
		branch, ok := canonicalStrictString(fields["branch"])
		if !ok {
			return nil, false
		}
		_ = out.field("branch", branch)
	case "commit":
		sha, ok := canonicalStrictString(fields["sha"])
		if !ok {
			return nil, false
		}
		_ = out.field("sha", sha)
		if raw, present := fields["title"]; present && !canonicalRawIsNull(raw) {
			title, ok := canonicalRawJSONStringValue(raw)
			if !ok {
				return nil, false
			}
			_ = out.field("title", title)
		} else {
			_ = out.rawField("title", []byte("null"))
		}
	case "custom":
		instructions, ok := canonicalStrictString(fields["instructions"])
		if !ok {
			return nil, false
		}
		_ = out.field("instructions", instructions)
	default:
		return nil, false
	}
	return out.finish(), true
}

// canonicalReviewFinding replays ReviewFinding's field order.
func canonicalReviewFinding(value json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(value)
	if err != nil {
		return nil, false
	}
	title, ok := canonicalStrictString(fields["title"])
	if !ok {
		return nil, false
	}
	body, ok := canonicalStrictString(fields["body"])
	if !ok {
		return nil, false
	}
	score, ok := canonicalFloat32JSON(fields["confidence_score"])
	if !ok {
		return nil, false
	}
	priority, ok := canonicalStrictInt(fields["priority"])
	if !ok || priority < -2147483648 || priority > 2147483647 {
		return nil, false
	}
	location, err := canonicalRawFields(fields["code_location"])
	if err != nil {
		return nil, false
	}
	filePath, ok := canonicalStrictString(location["absolute_file_path"])
	if !ok {
		return nil, false
	}
	lineRange, err := canonicalRawFields(location["line_range"])
	if err != nil {
		return nil, false
	}
	start, ok := canonicalStrictInt(lineRange["start"])
	if !ok || start < 0 || start > 4294967295 {
		return nil, false
	}
	end, ok := canonicalStrictInt(lineRange["end"])
	if !ok || end < 0 || end > 4294967295 {
		return nil, false
	}
	rng := newCanonicalJSONObject()
	_ = rng.field("start", start)
	_ = rng.field("end", end)
	place := newCanonicalJSONObject()
	_ = place.field("absolute_file_path", filePath)
	_ = place.rawField("line_range", rng.finish())
	out := newCanonicalJSONObject()
	_ = out.field("title", title)
	_ = out.field("body", body)
	_ = out.rawField("confidence_score", score)
	_ = out.field("priority", priority)
	_ = out.rawField("code_location", place.finish())
	return out.finish(), true
}

// canonicalReviewOutput replays ReviewOutputEvent's field order.
func canonicalReviewOutput(value json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(value)
	if err != nil {
		return nil, false
	}
	rawFindings, ok := fields["findings"]
	if !ok || canonicalRawIsNull(rawFindings) {
		return nil, false
	}
	var findings []json.RawMessage
	if err := json.Unmarshal(rawFindings, &findings); err != nil {
		return nil, false
	}
	converted := make([][]byte, 0, len(findings))
	for _, raw := range findings {
		finding, ok := canonicalReviewFinding(raw)
		if !ok {
			return nil, false
		}
		converted = append(converted, finding)
	}
	correctness, ok := canonicalStrictString(fields["overall_correctness"])
	if !ok {
		return nil, false
	}
	explanation, ok := canonicalStrictString(fields["overall_explanation"])
	if !ok {
		return nil, false
	}
	score, ok := canonicalFloat32JSON(fields["overall_confidence_score"])
	if !ok {
		return nil, false
	}
	out := newCanonicalJSONObject()
	_ = out.rawField("findings", canonicalContentArray(converted))
	_ = out.field("overall_correctness", correctness)
	_ = out.field("overall_explanation", explanation)
	_ = out.rawField("overall_confidence_score", score)
	return out.finish(), true
}

// canonicalFloat32JSON renders a JSON number the way serde_json/ryu render an
// f32: shortest round-tripping digits, decimal notation while the decimal
// exponent stays in [-4, 16), scientific notation with a signed exponent
// otherwise.
func canonicalFloat32JSON(value json.RawMessage) ([]byte, bool) {
	if len(value) == 0 || canonicalRawIsNull(value) {
		return nil, false
	}
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 {
		return nil, false
	}
	if first := trimmed[0]; first != '-' && (first < '0' || first > '9') {
		return nil, false
	}
	var parsed float64
	if err := json.Unmarshal(trimmed, &parsed); err != nil {
		return nil, false
	}
	number := float32(parsed)
	if math.IsInf(float64(number), 0) {
		return nil, false
	}
	scientific := strconv.FormatFloat(float64(number), 'e', -1, 32)
	mantissa, exponentText, found := strings.Cut(scientific, "e")
	if !found {
		return nil, false
	}
	exponent, err := strconv.Atoi(exponentText)
	if err != nil {
		return nil, false
	}
	negative := strings.HasPrefix(mantissa, "-")
	digits := strings.Replace(strings.TrimPrefix(mantissa, "-"), ".", "", 1)
	var builder strings.Builder
	if negative {
		builder.WriteByte('-')
	}
	if exponent >= -4 && exponent < 16 {
		if exponent >= 0 {
			whole := exponent + 1
			if whole >= len(digits) {
				builder.WriteString(digits)
				builder.WriteString(strings.Repeat("0", whole-len(digits)))
				builder.WriteString(".0")
			} else {
				builder.WriteString(digits[:whole])
				builder.WriteByte('.')
				builder.WriteString(digits[whole:])
			}
		} else {
			builder.WriteString("0.")
			builder.WriteString(strings.Repeat("0", -exponent-1))
			builder.WriteString(digits)
		}
	} else {
		builder.WriteString(digits[:1])
		if len(digits) > 1 {
			builder.WriteByte('.')
			builder.WriteString(digits[1:])
		}
		builder.WriteByte('e')
		if exponent >= 0 {
			builder.WriteByte('+')
		}
		builder.WriteString(strconv.Itoa(exponent))
	}
	return []byte(builder.String()), true
}

// writeReviewMode converts entered_review_mode and exited_review_mode events
// into their completed items.
func (w *canonicalReplayWriter) writeReviewMode(entry canonicalReplayLine, entered bool) error {
	fields, err := canonicalRawFields(entry.payload)
	if err != nil {
		return err
	}
	itemID := ""
	if text, ok := canonicalStrictString(fields["item_id"]); ok {
		itemID = text
	}
	if itemID == "" {
		itemID = w.itemID()
	}
	out := newCanonicalJSONObject()
	if entered {
		target, present := fields["target"]
		if !present || canonicalRawIsNull(target) {
			prompt, ok := canonicalStrictString(fields["prompt"])
			if !ok {
				return nil
			}
			custom := newCanonicalJSONObject()
			_ = custom.field("type", "custom")
			_ = custom.field("instructions", prompt)
			target = custom.finish()
		}
		convertedTarget, ok := canonicalReviewTarget(target)
		if !ok {
			return nil
		}
		hint := "Review requested."
		if raw, present := fields["user_facing_hint"]; present && !canonicalRawIsNull(raw) {
			text, ok := canonicalRawJSONStringValue(raw)
			if !ok {
				return nil
			}
			hint = text
		}
		_ = out.field("type", "EnteredReviewMode")
		_ = out.field("id", itemID)
		_ = out.rawField("target", convertedTarget)
		_ = out.field("user_facing_hint", hint)
	} else {
		reviewOutput := []byte("null")
		if raw, present := fields["review_output"]; present && !canonicalRawIsNull(raw) {
			converted, ok := canonicalReviewOutput(raw)
			if !ok {
				return nil
			}
			reviewOutput = converted
		}
		_ = out.field("type", "ExitedReviewMode")
		_ = out.field("id", itemID)
		_ = out.rawField("review_output", reviewOutput)
	}
	turnID, err := w.legacyEventTurnID(entry)
	if err != nil {
		return err
	}
	return w.writeItemCompleted(entry.timestamp, turnID, out.finish())
}

// canonicalUUID reports whether value is a canonical 8-4-4-4-12 UUID string,
// matching ThreadId's uuid::Uuid deserializer.
func canonicalUUID(value json.RawMessage) (string, bool) {
	text, ok := canonicalStrictString(value)
	if !ok || len(text) != 36 {
		return "", false
	}
	for index, char := range text {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return "", false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
				return "", false
			}
		}
	}
	return text, true
}

// canonicalAgentPath validates AgentPath's absolute /root or /morpheus shape.
func canonicalAgentPath(value json.RawMessage) (string, bool) {
	text, ok := canonicalStrictString(value)
	if !ok {
		return "", false
	}
	if text == "/morpheus" || text == "/root" {
		return text, true
	}
	if !strings.HasPrefix(text, "/root/") || strings.HasSuffix(text, "/") {
		return "", false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(text, "/root/"), "/") {
		if segment == "" {
			return "", false
		}
		for _, char := range segment {
			valid := (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_'
			if !valid {
				return "", false
			}
		}
	}
	return text, true
}

// writeSubAgentActivity converts a legacy sub_agent_activity event into the
// SubAgentActivity completed item.
func (w *canonicalReplayWriter) writeSubAgentActivity(entry canonicalReplayLine) error {
	fields, err := canonicalRawFields(entry.payload)
	if err != nil {
		return err
	}
	eventID, ok := canonicalStrictString(fields["event_id"])
	if !ok {
		return nil
	}
	kind, ok := canonicalStrictString(fields["kind"])
	if !ok {
		return nil
	}
	switch kind {
	case "started", "interacted", "interrupted", "completed":
	default:
		return nil
	}
	agentThreadID, ok := canonicalUUID(fields["agent_thread_id"])
	if !ok {
		return nil
	}
	agentPath, ok := canonicalAgentPath(fields["agent_path"])
	if !ok {
		return nil
	}
	out := newCanonicalJSONObject()
	_ = out.field("type", "SubAgentActivity")
	for _, key := range []string{"model", "reasoning_effort"} {
		text := ""
		hasText := false
		if raw, present := fields[key]; present && !canonicalRawIsNull(raw) {
			text, hasText = canonicalRawJSONStringValue(raw)
			if !hasText {
				return nil
			}
		}
		if hasText {
			_ = out.field(key, text)
		} else {
			_ = out.rawField(key, []byte("null"))
		}
	}
	_ = out.field("id", eventID)
	_ = out.field("kind", kind)
	_ = out.field("agent_thread_id", agentThreadID)
	_ = out.field("agent_path", agentPath)
	turnID, err := w.legacyEventTurnID(entry)
	if err != nil {
		return err
	}
	return w.writeItemCompleted(entry.timestamp, turnID, out.finish())
}

// canonicalDynamicContentItem replays DynamicToolCallOutputContentItem's
// internally tagged camelCase enum.
func canonicalDynamicContentItem(value json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(value)
	if err != nil {
		return nil, false
	}
	kind, ok := canonicalStrictString(fields["type"])
	if !ok {
		return nil, false
	}
	out := newCanonicalJSONObject()
	_ = out.field("type", kind)
	switch kind {
	case "inputText":
		text, ok := canonicalStrictString(fields["text"])
		if !ok {
			return nil, false
		}
		_ = out.field("text", text)
	case "inputImage":
		url, ok := canonicalStrictString(fields["imageUrl"])
		if !ok {
			return nil, false
		}
		_ = out.field("imageUrl", url)
	case "inputAudio":
		url, ok := canonicalStrictString(fields["audioUrl"])
		if !ok {
			return nil, false
		}
		_ = out.field("audioUrl", url)
	default:
		return nil, false
	}
	return out.finish(), true
}

// writeDynamicToolCall converts a legacy dynamic_tool_call_response event into
// the DynamicToolCall completed item.
func (w *canonicalReplayWriter) writeDynamicToolCall(entry canonicalReplayLine) error {
	fields, err := canonicalRawFields(entry.payload)
	if err != nil {
		return err
	}
	callID, ok := canonicalStrictString(fields["call_id"])
	if !ok {
		return nil
	}
	tool, ok := canonicalStrictString(fields["tool"])
	if !ok {
		return nil
	}
	arguments, ok := fields["arguments"]
	if !ok {
		return nil
	}
	success, ok := canonicalRawJSONBool(fields["success"])
	if !ok {
		return nil
	}
	rawContent, ok := fields["content_items"]
	if !ok || canonicalRawIsNull(rawContent) {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(rawContent, &items); err != nil {
		return nil
	}
	converted := make([][]byte, 0, len(items))
	for _, raw := range items {
		item, ok := canonicalDynamicContentItem(raw)
		if !ok {
			return nil
		}
		converted = append(converted, item)
	}
	duration, ok := canonicalDuration(fields["duration"])
	if !ok {
		return nil
	}
	out := newCanonicalJSONObject()
	_ = out.field("type", "DynamicToolCall")
	_ = out.field("id", callID)
	if raw, present := fields["namespace"]; present && !canonicalRawIsNull(raw) {
		text, ok := canonicalRawJSONStringValue(raw)
		if !ok {
			return nil
		}
		_ = out.field("namespace", text)
	}
	_ = out.field("tool", tool)
	_ = out.rawField("arguments", arguments)
	if success {
		_ = out.field("status", "completed")
	} else {
		_ = out.field("status", "failed")
	}
	_ = out.rawField("content_items", canonicalContentArray(converted))
	_ = out.field("success", success)
	if raw, present := fields["error"]; present && !canonicalRawIsNull(raw) {
		text, ok := canonicalRawJSONStringValue(raw)
		if !ok {
			return nil
		}
		_ = out.field("error", text)
	}
	_ = out.rawField("duration", duration)
	turnID, err := w.legacyEventTurnID(entry)
	if err != nil {
		return err
	}
	return w.writeItemCompleted(entry.timestamp, turnID, out.finish())
}

// canonicalAbsolutePath replays AbsolutePathBuf: the value must already be
// absolute, and serialization writes the lexically cleaned path (Windows
// separators on this platform).
func canonicalAbsolutePath(value json.RawMessage) (string, bool) {
	text, ok := canonicalStrictString(value)
	if !ok {
		return "", false
	}
	if runtime.GOOS != "windows" {
		if !strings.HasPrefix(text, "/") {
			return "", false
		}
		return filepath.Clean(text), true
	}
	cleaned := filepath.Clean(strings.ReplaceAll(text, "/", `\`))
	if len(cleaned) < 3 {
		return "", false
	}
	drive := (cleaned[0] >= 'A' && cleaned[0] <= 'Z') || (cleaned[0] >= 'a' && cleaned[0] <= 'z')
	if !(drive && cleaned[1] == ':' && cleaned[2] == '\\') && !strings.HasPrefix(cleaned, `\\`) {
		return "", false
	}
	return cleaned, true
}

// canonicalImageGenerationFailure replays the tagged ImageGenerationFailure
// enum.
func canonicalImageGenerationFailure(value json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(value)
	if err != nil {
		return nil, false
	}
	kind, ok := canonicalStrictString(fields["type"])
	if !ok || kind != "usageLimitExceeded" {
		return nil, false
	}
	limitID, ok := canonicalStrictString(fields["limitId"])
	if !ok {
		return nil, false
	}
	out := newCanonicalJSONObject()
	_ = out.field("type", kind)
	_ = out.field("limitId", limitID)
	if raw, present := fields["resetsAt"]; present && !canonicalRawIsNull(raw) {
		resetsAt, ok := canonicalStrictInt(raw)
		if !ok {
			return nil, false
		}
		_ = out.field("resetsAt", resetsAt)
	} else {
		_ = out.rawField("resetsAt", []byte("null"))
	}
	return out.finish(), true
}

// canonicalImageGenerationInputImages replays the camelCase input image list.
func canonicalImageGenerationInputImages(value json.RawMessage) ([]byte, bool) {
	var entries []json.RawMessage
	if err := json.Unmarshal(value, &entries); err != nil {
		return nil, false
	}
	converted := make([][]byte, 0, len(entries))
	for _, raw := range entries {
		fields, err := canonicalRawFields(raw)
		if err != nil {
			return nil, false
		}
		path, ok := canonicalAbsolutePath(fields["path"])
		if !ok {
			return nil, false
		}
		sha, ok := canonicalStrictString(fields["sha256"])
		if !ok {
			return nil, false
		}
		entry := newCanonicalJSONObject()
		_ = entry.field("path", path)
		_ = entry.field("sha256", sha)
		converted = append(converted, entry.finish())
	}
	return canonicalContentArray(converted), true
}

// writeImageGeneration converts a legacy image_generation_end event into the
// Extension(image_gen.generation) completed item.
func (w *canonicalReplayWriter) writeImageGeneration(entry canonicalReplayLine) error {
	fields, err := canonicalRawFields(entry.payload)
	if err != nil {
		return err
	}
	callID, ok := canonicalStrictString(fields["call_id"])
	if !ok {
		return nil
	}
	status, ok := canonicalStrictString(fields["status"])
	if !ok {
		return nil
	}
	out := newCanonicalJSONObject()
	_ = out.field("type", "Extension")
	_ = out.field("kind", "image_gen.generation")
	_ = out.field("id", callID)
	_ = out.field("status", status)
	if raw, present := fields["revised_prompt"]; present && !canonicalRawIsNull(raw) {
		text, ok := canonicalRawJSONStringValue(raw)
		if !ok {
			return nil
		}
		_ = out.field("revisedPrompt", text)
	} else {
		_ = out.rawField("revisedPrompt", []byte("null"))
	}
	if raw, present := fields["fileId"]; present && !canonicalRawIsNull(raw) {
		text, ok := canonicalRawJSONStringValue(raw)
		if !ok {
			return nil
		}
		_ = out.field("fileId", text)
	} else if raw, present := fields["result"]; present && !canonicalRawIsNull(raw) {
		text, ok := canonicalRawJSONStringValue(raw)
		if !ok {
			return nil
		}
		_ = out.field("result", text)
	} else {
		return nil
	}
	transparent := []byte("null")
	if raw, present := fields["transparent_background"]; present && !canonicalRawIsNull(raw) {
		flag, ok := canonicalRawJSONBool(raw)
		if !ok {
			return nil
		}
		transparent = []byte(strconv.FormatBool(flag))
	}
	_ = out.rawField("transparentBackground", transparent)
	if raw, present := fields["failure"]; present && !canonicalRawIsNull(raw) {
		converted, ok := canonicalImageGenerationFailure(raw)
		if !ok {
			return nil
		}
		_ = out.rawField("failure", converted)
	} else {
		_ = out.rawField("failure", []byte("null"))
	}
	if raw, present := fields["saved_path"]; present && !canonicalRawIsNull(raw) {
		path, ok := canonicalAbsolutePath(raw)
		if !ok {
			return nil
		}
		_ = out.field("savedPath", path)
	}
	if raw, present := fields["input_images"]; present && !canonicalRawIsNull(raw) {
		images, ok := canonicalImageGenerationInputImages(raw)
		if !ok {
			return nil
		}
		if string(images) != "[]" {
			_ = out.rawField("inputImages", images)
		}
	}
	if raw, present := fields["generation_id"]; present && !canonicalRawIsNull(raw) {
		text, ok := canonicalRawJSONStringValue(raw)
		if !ok {
			return nil
		}
		_ = out.field("generationId", text)
	} else {
		_ = out.rawField("generationId", []byte("null"))
	}
	turnID, err := w.ensureTurn(entry.timestamp, entry.sourceIndex)
	if err != nil {
		return err
	}
	w.reasoning = nil
	return w.writeItemCompleted(entry.timestamp, turnID, out.finish())
}

// canonicalTurnItemRequiredFields lists the fields 0.166's TurnItem variants
// cannot decode without. Rust parses every legacy record into the modern types
// before migrating it and silently drops the whole line when that fails, so a
// legacy item_completed carrying an older item shape never reaches the
// paginated rollout.
var canonicalTurnItemRequiredFields = map[string][]string{
	"UserMessage":         {"id", "content"},
	"FunctionCallOutput":  {"id", "name", "output"},
	"HookPrompt":          {"id", "fragments"},
	"AgentMessage":        {"id", "content"},
	"Plan":                {"id", "text"},
	"Reasoning":           {"id", "summary_text"},
	"CommandExecution":    {"id", "command", "cwd", "parsed_cmd", "source", "status"},
	"DynamicToolCall":     {"id", "tool", "arguments", "status"},
	"CollabAgentToolCall": {"id", "tool", "status", "sender_thread_id"},
	"SubAgentActivity":    {"model", "reasoning_effort", "id", "kind", "agent_thread_id", "agent_path"},
	"WebSearch":           {"id", "query", "action"},
	"ImageView":           {"id", "path"},
	"ImageGeneration":     {"id", "status", "result"},
	"EnteredReviewMode":   {"id", "target", "user_facing_hint"},
	"ExitedReviewMode":    {"id", "review_output"},
	"FileChange":          {"id", "changes"},
	"McpToolCall":         {"id", "server", "tool", "arguments", "status"},
	"ContextCompaction":   {"id"},
}

// canonicalTurnItemDecodes reports whether an item_completed payload would
// deserialize into a modern TurnItem. Extension items keep their own schema, so
// they are carried over unvalidated.
func canonicalTurnItemDecodes(source json.RawMessage) bool {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return false
	}
	itemRaw, ok := fields["item"]
	if !ok || canonicalRawIsNull(itemRaw) {
		return false
	}
	item, err := canonicalRawFields(itemRaw)
	if err != nil {
		return false
	}
	itemType := canonicalRawString(itemRaw, "type")
	if itemType == "Extension" {
		return true
	}
	required, known := canonicalTurnItemRequiredFields[itemType]
	if !known {
		return false
	}
	for _, key := range required {
		if _, ok := item[key]; !ok {
			return false
		}
	}
	return true
}

// writePatchApplyEnd converts a legacy patch_apply_end event into the
// FileChange completed item paginated rollouts persist.
func (w *canonicalReplayWriter) writePatchApplyEnd(entry canonicalReplayLine) error {
	fields, err := canonicalRawFields(entry.payload)
	if err != nil {
		return err
	}
	// Rust parses the record into the modern PatchApplyEndEvent first; a record
	// missing a required field never reaches the canonicalizer and the whole
	// line is discarded.
	switch canonicalRawString(entry.payload, "status") {
	case "completed", "failed", "declined":
	default:
		return nil
	}
	for _, key := range []string{"call_id", "stdout", "stderr", "success"} {
		if _, ok := fields[key]; !ok {
			return nil
		}
	}
	turnID, err := w.legacyEventTurnID(entry)
	if err != nil {
		return err
	}
	item, err := canonicalFileChangeItem(fields)
	if err != nil {
		return err
	}
	return w.writeItemCompleted(entry.timestamp, turnID, item)
}

// canonicalFileChangeItem builds TurnItem::FileChange from a legacy
// patch_apply_end payload (legacy_event.rs completed_item).
func canonicalFileChangeItem(fields map[string]json.RawMessage) ([]byte, error) {
	callID := ""
	if value, ok := fields["call_id"]; ok && !canonicalRawIsNull(value) {
		if err := json.Unmarshal(value, &callID); err != nil {
			return nil, err
		}
	}
	status := ""
	if value, ok := fields["status"]; ok && !canonicalRawIsNull(value) {
		if err := json.Unmarshal(value, &status); err != nil {
			return nil, err
		}
	}
	out := newCanonicalJSONObject()
	if err := out.field("type", "FileChange"); err != nil {
		return nil, err
	}
	if err := out.field("id", callID); err != nil {
		return nil, err
	}
	changes := []byte("{}")
	if value, ok := fields["changes"]; ok && !canonicalRawIsNull(value) {
		changes = value
	}
	if err := out.rawField("changes", changes); err != nil {
		return nil, err
	}
	if err := out.field("status", status); err != nil {
		return nil, err
	}
	for _, key := range []string{"stdout", "stderr"} {
		text := ""
		if value, ok := fields[key]; ok && !canonicalRawIsNull(value) {
			if err := json.Unmarshal(value, &text); err != nil {
				return nil, err
			}
		}
		if text == "" {
			continue
		}
		if err := out.field(key, text); err != nil {
			return nil, err
		}
	}
	return out.finish(), nil
}

func (w *canonicalReplayWriter) writeReasoning(entry canonicalReplayLine) error {
	text := canonicalRawString(entry.payload, "text")
	if text == "" {
		return nil
	}
	turnID, err := w.ensureTurn(entry.timestamp, entry.sourceIndex)
	if err != nil {
		return err
	}
	// ensureTurn clears the accumulator when it opens a fresh implicit turn,
	// mirroring Rust's write_reasoning which runs after ensure_turn.
	if w.reasoning == nil {
		w.reasoning = &canonicalReasoningState{id: w.itemID()}
	}
	if entry.eventType == "agent_reasoning" {
		w.reasoning.summary = append(w.reasoning.summary, text)
	} else {
		w.reasoning.rawContent = append(w.reasoning.rawContent, text)
	}
	item, buildErr := canonicalReasoningItem(w.reasoning)
	if buildErr != nil {
		return buildErr
	}
	return w.writeItemCompleted(entry.timestamp, turnID, item)
}

func (w *canonicalReplayWriter) writeCommandExecution(entry canonicalReplayLine) error {
	turnID, err := w.legacyEventTurnID(entry)
	if err != nil {
		return err
	}
	item, err := canonicalCommandExecutionItem(entry.payload)
	if err != nil {
		return err
	}
	return w.writeItemCompleted(entry.timestamp, turnID, item)
}

// canonicalTaskStartedPayload re-serializes a turn-started event the way the
// protocol type does: optional attribution fields are dropped when unset and
// model_context_window/collaboration_mode_kind always serialize.
func canonicalTaskStartedPayload(source json.RawMessage) ([]byte, error) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, err
	}
	out := newCanonicalJSONObject()
	if err := out.field("type", "task_started"); err != nil {
		return nil, err
	}
	if err := out.field("turn_id", canonicalRawString(source, "turn_id")); err != nil {
		return nil, err
	}
	for _, key := range []string{"root_turn_id", "trace_id", "started_at"} {
		if value, ok := fields[key]; ok && !canonicalRawIsNull(value) {
			if err := out.rawField(key, value); err != nil {
				return nil, err
			}
		}
	}
	if value, ok := fields["model_context_window"]; ok {
		if err := out.rawField("model_context_window", value); err != nil {
			return nil, err
		}
	} else if err := out.rawField("model_context_window", []byte("null")); err != nil {
		return nil, err
	}
	mode := "default"
	if value, ok := fields["collaboration_mode_kind"]; ok && !canonicalRawIsNull(value) {
		mode = canonicalRawString(source, "collaboration_mode_kind")
	}
	if err := out.field("collaboration_mode_kind", mode); err != nil {
		return nil, err
	}
	return out.finish(), nil
}

func canonicalTaskCompletePayload(source json.RawMessage) ([]byte, error) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, err
	}
	out := newCanonicalJSONObject()
	if err := out.field("type", "task_complete"); err != nil {
		return nil, err
	}
	if value, ok := fields["root_turn_id"]; ok && !canonicalRawIsNull(value) {
		if err := out.rawField("root_turn_id", value); err != nil {
			return nil, err
		}
	}
	if err := out.field("turn_id", canonicalRawString(source, "turn_id")); err != nil {
		return nil, err
	}
	if value, ok := fields["last_agent_message"]; ok {
		if err := out.rawField("last_agent_message", value); err != nil {
			return nil, err
		}
	} else if err := out.rawField("last_agent_message", []byte("null")); err != nil {
		return nil, err
	}
	for _, key := range []string{"error", "started_at", "completed_at", "duration_ms", "time_to_first_token_ms"} {
		if value, ok := fields[key]; ok && !canonicalRawIsNull(value) {
			if err := out.rawField(key, value); err != nil {
				return nil, err
			}
		}
	}
	return out.finish(), nil
}

func canonicalTurnAbortedPayload(source json.RawMessage, turnID string) ([]byte, error) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, err
	}
	out := newCanonicalJSONObject()
	if err := out.field("type", "turn_aborted"); err != nil {
		return nil, err
	}
	if turnID != "" {
		if err := out.field("turn_id", turnID); err != nil {
			return nil, err
		}
	}
	for _, key := range []string{"reason"} {
		if value, ok := fields[key]; ok && !canonicalRawIsNull(value) {
			if err := out.rawField(key, value); err != nil {
				return nil, err
			}
		}
	}
	return out.finish(), nil
}

// canonicalTokenCountPayload keeps the modern token_count shape: the retired
// input_tokens/output_tokens fields are replaced by info/rate_limits.
func canonicalTokenCountPayload(source json.RawMessage) ([]byte, error) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, err
	}
	out := newCanonicalJSONObject()
	if err := out.field("type", "token_count"); err != nil {
		return nil, err
	}
	for _, key := range []string{"info", "rate_limits"} {
		if value, ok := fields[key]; ok && !canonicalRawIsNull(value) {
			if err := out.rawField(key, value); err != nil {
				return nil, err
			}
		} else if err := out.rawField(key, []byte("null")); err != nil {
			return nil, err
		}
	}
	return out.finish(), nil
}

func canonicalRawFields(source json.RawMessage) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(source, &fields); err != nil {
		return nil, fmt.Errorf("parse legacy event payload: %w", err)
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	return fields, nil
}

func canonicalRawIsNull(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) == 0 || string(trimmed) == "null"
}

// canonicalUserMessageItem builds TurnItem::UserMessage (legacy_event.rs
// user_message_item) with synthesized stable ids and Rust's content order.
func canonicalUserMessageItem(source json.RawMessage, itemID string) []byte {
	fields, _ := canonicalRawFields(source)
	content := make([][]byte, 0, 2)
	message := canonicalRawString(source, "message")
	if strings.TrimSpace(message) != "" {
		entry := newCanonicalJSONObject()
		_ = entry.field("type", "text")
		_ = entry.field("text", message)
		if value, ok := fields["text_elements"]; ok && !canonicalRawIsNull(value) {
			_ = entry.rawField("text_elements", value)
		} else {
			_ = entry.rawField("text_elements", []byte("[]"))
		}
		content = append(content, entry.finish())
	}
	out := newCanonicalJSONObject()
	_ = out.field("type", "UserMessage")
	_ = out.field("id", itemID)
	clientID := canonicalRawString(source, "client_id")
	if clientID == "" {
		clientID = canonicalRawString(source, "clientId")
	}
	if clientID != "" {
		_ = out.field("client_id", clientID)
	}
	_ = out.field("content", json.RawMessage(canonicalContentArray(content)))
	return out.finish()
}

func canonicalContentArray(entries [][]byte) []byte {
	var buffer bytes.Buffer
	buffer.WriteByte('[')
	for index, entry := range entries {
		if index > 0 {
			buffer.WriteByte(',')
		}
		buffer.Write(entry)
	}
	buffer.WriteByte(']')
	return buffer.Bytes()
}

// canonicalAgentMessageItem builds TurnItem::AgentMessage.
func canonicalAgentMessageItem(source json.RawMessage, itemID string) ([]byte, error) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, err
	}
	content := newCanonicalJSONObject()
	if err := content.field("type", "Text"); err != nil {
		return nil, err
	}
	if err := content.field("text", canonicalRawString(source, "message")); err != nil {
		return nil, err
	}
	out := newCanonicalJSONObject()
	if err := out.field("type", "AgentMessage"); err != nil {
		return nil, err
	}
	if err := out.field("id", itemID); err != nil {
		return nil, err
	}
	if err := out.field("content", []json.RawMessage{json.RawMessage(content.finish())}); err != nil {
		return nil, err
	}
	for _, key := range []string{"phase", "memory_citation", "delivery", "questions"} {
		if value, ok := fields[key]; ok && !canonicalRawIsNull(value) {
			if err := out.rawField(key, value); err != nil {
				return nil, err
			}
		}
	}
	return out.finish(), nil
}

// canonicalReasoningItem builds TurnItem::Reasoning from the accumulated
// summary/raw reasoning text.
func canonicalReasoningItem(state *canonicalReasoningState) ([]byte, error) {
	out := newCanonicalJSONObject()
	if err := out.field("type", "Reasoning"); err != nil {
		return nil, err
	}
	if err := out.field("id", state.id); err != nil {
		return nil, err
	}
	summary := state.summary
	if summary == nil {
		summary = []string{}
	}
	rawContent := state.rawContent
	if rawContent == nil {
		rawContent = []string{}
	}
	if err := out.field("summary_text", summary); err != nil {
		return nil, err
	}
	if err := out.field("raw_content", rawContent); err != nil {
		return nil, err
	}
	return out.finish(), nil
}

// canonicalCommandExecutionItem builds TurnItem::CommandExecution from a legacy
// exec_command_end event, normalizing the command cwd to a PathUri like Rust's
// LegacyAppPathString conversion.
func canonicalCommandExecutionItem(source json.RawMessage) ([]byte, error) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, err
	}
	out := newCanonicalJSONObject()
	if err := out.field("type", "CommandExecution"); err != nil {
		return nil, err
	}
	for _, key := range []string{"model_context", "sandbox_type"} {
		if value, ok := fields[key]; ok && !canonicalRawIsNull(value) {
			if err := out.rawField(key, value); err != nil {
				return nil, err
			}
		}
	}
	if err := out.field("id", canonicalRawString(source, "call_id")); err != nil {
		return nil, err
	}
	for _, key := range []string{"plugin_id", "script_path", "process_id"} {
		if value, ok := fields[key]; ok && !canonicalRawIsNull(value) {
			if err := out.rawField(key, value); err != nil {
				return nil, err
			}
		}
	}
	if value, ok := fields["command"]; ok && !canonicalRawIsNull(value) {
		if err := out.rawField("command", value); err != nil {
			return nil, err
		}
	} else if err := out.rawField("command", []byte("[]")); err != nil {
		return nil, err
	}
	cwd := canonicalRawString(source, "cwd")
	if cwd != "" {
		if uri, uriErr := canonicalPathURI(cwd); uriErr == nil {
			cwd = uri
		}
		if err := out.field("cwd", cwd); err != nil {
			return nil, err
		}
	}
	if value, ok := fields["parsed_cmd"]; ok && !canonicalRawIsNull(value) {
		if err := out.rawField("parsed_cmd", value); err != nil {
			return nil, err
		}
	} else if err := out.rawField("parsed_cmd", []byte("[]")); err != nil {
		return nil, err
	}
	sourceKind := canonicalRawString(source, "source")
	if sourceKind == "" {
		sourceKind = "agent"
	}
	if err := out.field("source", sourceKind); err != nil {
		return nil, err
	}
	if value, ok := fields["interaction_input"]; ok && !canonicalRawIsNull(value) {
		if err := out.rawField("interaction_input", value); err != nil {
			return nil, err
		}
	}
	status := canonicalRawString(source, "status")
	if status == "" {
		status = "completed"
		if value, ok := fields["success"]; ok {
			var success bool
			if json.Unmarshal(value, &success) == nil && !success {
				status = "failed"
			}
		}
	}
	if err := out.field("status", status); err != nil {
		return nil, err
	}
	if output := canonicalRawString(source, "aggregated_output"); output != "" {
		if err := out.field("aggregated_output", output); err != nil {
			return nil, err
		}
	}
	for _, key := range []string{"exit_code", "duration"} {
		if value, ok := fields[key]; ok && !canonicalRawIsNull(value) {
			if err := out.rawField(key, value); err != nil {
				return nil, err
			}
		}
	}
	return out.finish(), nil
}

func canonicalPathURI(path string) (string, error) {
	legacy := utils.NewLegacyAppPathString(path)
	uri, ok := legacy.ToInferredPathURI()
	if !ok || uri == nil {
		return "", fmt.Errorf("path %q is not an absolute POSIX or Windows path", path)
	}
	return uri.String(), nil
}

// writeCanonicalReplay replays the legacy records into the staged file and
// returns Rust's migration byte accounting plus the first free ordinal.
func writeCanonicalReplay(file *os.File, meta *SessionMeta, headerTimestamp string, rawLines [][]byte, sourceBytes uint64) (uint64, uint64, error) {
	planned, firstMarker, err := planCanonicalReplay(rawLines)
	if err != nil {
		return 0, 0, err
	}
	entries := applyCanonicalRollbackPlan(planned)
	headerBytes := uint64(0)
	for _, raw := range rawLines {
		var decoded canonicalRawLine
		if err := json.Unmarshal(raw, &decoded); err != nil {
			continue
		}
		if decoded.Type == "session_meta" {
			headerBytes = uint64(len(raw)) + 1
			break
		}
	}
	if headerTimestamp == "" && meta != nil {
		headerTimestamp = meta.Timestamp
	}
	writer := newCanonicalReplayWriter(file, canonicalThreadID(meta))
	if err := writer.writeHeadSessionMeta(headerTimestamp, meta); err != nil {
		return 0, 0, err
	}
	headBytes := writer.bytesWritten
	_ = headBytes
	// A rollout that still carries a rollback marker is converted twice: Rust
	// first streams an unplanned attempt, discovers the marker, plans the
	// rollback by re-reading the source, then streams the planned rollout. The
	// rate limiter counts every pass, so mirror that accounting here.
	rollbackExtra := uint64(0)
	if firstMarker >= 0 {
		aborted, abortedErr := canonicalAbortedAttemptBytes(planned[:firstMarker], meta, headerTimestamp)
		if abortedErr != nil {
			return 0, 0, abortedErr
		}
		rollbackExtra = canonicalSourceBytesThrough(rawLines, planned[firstMarker].sourceIndex) + aborted + sourceBytes
	}
	lastTimestamp := headerTimestamp
	for i := range entries {
		if strings.TrimSpace(entries[i].timestamp) != "" {
			lastTimestamp = entries[i].timestamp
		}
		if err := writer.replay(entries[i]); err != nil {
			return 0, 0, err
		}
	}
	if err := writer.finishImplicitTurn(lastTimestamp); err != nil {
		return 0, 0, err
	}
	return sourceBytes + headerBytes + 2*writer.bytesWritten + rollbackExtra, writer.nextOrdinal, nil
}

// canonicalAbortedAttemptBytes measures the staged bytes the unplanned first
// conversion attempt wrote before it stopped at the rollback marker.
func canonicalAbortedAttemptBytes(entries []canonicalReplayLine, meta *SessionMeta, headerTimestamp string) (uint64, error) {
	var sink bytes.Buffer
	writer := newCanonicalReplayWriter(&sink, canonicalThreadID(meta))
	if err := writer.writeHeadSessionMeta(headerTimestamp, meta); err != nil {
		return 0, err
	}
	for i := range entries {
		if err := writer.replay(entries[i]); err != nil {
			return 0, err
		}
	}
	return writer.bytesWritten, nil
}

// canonicalSourceBytesThrough sums the source bytes of the records read up to
// and including sourceIndex.
func canonicalSourceBytesThrough(rawLines [][]byte, sourceIndex uint64) uint64 {
	var total uint64
	for index, raw := range rawLines {
		if uint64(index) > sourceIndex {
			break
		}
		total += uint64(len(raw)) + 1
	}
	return total
}

func canonicalThreadID(meta *SessionMeta) string {
	if meta == nil {
		return ""
	}
	return strings.TrimSpace(meta.ID)
}
