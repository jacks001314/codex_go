package rollout

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
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
		raw = bytes.TrimRight(raw, "\r\n")
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		lines = append(lines, append([]byte(nil), raw...))
		decompressed += uint64(len(raw)) + 1
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
		return w.writeRecordWithMetadata(entry.timestamp, entry.lineType, entry.payload, entry.metadata)
	}
	return w.replayEvent(entry)
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
	default:
		return w.writeRecord(entry.timestamp, "event_msg", entry.payload)
	}
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
	turnID := canonicalRawString(entry.payload, "turn_id")
	if turnID == "" || turnID == w.activeTurnID {
		ensured, err := w.ensureTurn(entry.timestamp, entry.sourceIndex)
		if err != nil {
			return err
		}
		turnID = ensured
	} else {
		w.reasoning = nil
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
