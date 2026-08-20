package state

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"codex_go/rollout"
)

type ThreadHistoryProjectionState struct {
	NextRolloutByteOffset uint64
	NextRolloutOrdinal    uint64
}

const maxSQLiteInteger = uint64(1<<63 - 1)

type threadHistoryTurnChange struct {
	TurnID      string
	Status      string
	Error       json.RawMessage
	StartedAt   *int64
	CompletedAt *int64
	DurationMS  *int64
}

type threadHistoryItemChange struct {
	TurnID        string
	ItemID        string
	ItemType      string
	ItemJSON      json.RawMessage
	StartedAtMS   *int64
	CompletedAtMS *int64
}

type threadHistoryChanges struct {
	Turns []threadHistoryTurnChange
	Items []threadHistoryItemChange
}

type threadHistoryEvent struct {
	Type               string          `json:"type"`
	TurnID             string          `json:"turn_id"`
	TurnIDCamel        string          `json:"turnId"`
	StartedAt          *int64          `json:"started_at"`
	StartedAtCamel     *int64          `json:"startedAt"`
	CompletedAt        *int64          `json:"completed_at"`
	CompletedAtCamel   *int64          `json:"completedAt"`
	DurationMS         *int64          `json:"duration_ms"`
	DurationMSCamel    *int64          `json:"durationMs"`
	StartedAtMS        *int64          `json:"started_at_ms"`
	StartedAtMSCamel   *int64          `json:"startedAtMs"`
	CompletedAtMS      *int64          `json:"completed_at_ms"`
	CompletedAtMSCamel *int64          `json:"completedAtMs"`
	Error              json.RawMessage `json:"error"`
	Item               json.RawMessage `json:"item"`
}

func LoadThreadHistoryProjectionState(ctx context.Context, db *sql.DB, threadID string) (*ThreadHistoryProjectionState, error) {
	if db == nil {
		return nil, errors.New("thread history database is nil")
	}
	var byteOffset, ordinal int64
	err := db.QueryRowContext(ctx, `
SELECT next_rollout_byte_offset, next_rollout_ordinal
FROM thread_history_projection_state
WHERE thread_id = ?`, threadID).Scan(&byteOffset, &ordinal)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read thread history projection: %w", err)
	}
	if byteOffset < 0 || ordinal < 0 {
		return nil, fmt.Errorf("thread history projection for %s has a negative checkpoint", threadID)
	}
	return &ThreadHistoryProjectionState{NextRolloutByteOffset: uint64(byteOffset), NextRolloutOrdinal: uint64(ordinal)}, nil
}

// MaterializeThreadHistory projects a newline-terminated rollout suffix and
// advances its byte/ordinal checkpoint atomically with the projected rows.
func MaterializeThreadHistory(ctx context.Context, db *sql.DB, threadID string, rolloutPath string, initialOrdinal uint64, subagentHistoryStartOrdinal *uint64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	threadID = strings.TrimSpace(threadID)
	if db == nil {
		return errors.New("thread history database is nil")
	}
	if threadID == "" {
		return errors.New("thread id is required")
	}
	checkpoint, err := LoadThreadHistoryProjectionState(ctx, db, threadID)
	if err != nil {
		return err
	}
	startOffset, expectedOrdinal := uint64(0), initialOrdinal
	if checkpoint != nil {
		startOffset = checkpoint.NextRolloutByteOffset
		expectedOrdinal = checkpoint.NextRolloutOrdinal
	}
	validate := func(line *rollout.Line) error {
		if subagentHistoryStartOrdinal != nil && line.Ordinal != nil && *line.Ordinal < *subagentHistoryStartOrdinal {
			return nil
		}
		_, err := projectThreadHistoryLine(line)
		return err
	}
	steps, nextOffset, err := rollout.ReadProjectionStepsValidated(rolloutPath, startOffset, expectedOrdinal, validate)
	if err != nil {
		return fmt.Errorf("read thread history projection: %w", err)
	}
	if len(steps) == 0 && startOffset == nextOffset {
		return nil
	}
	return applyThreadHistoryProjection(ctx, db, threadID, startOffset, nextOffset, initialOrdinal, subagentHistoryStartOrdinal, steps)
}

func applyThreadHistoryProjection(ctx context.Context, db *sql.DB, threadID string, startOffset uint64, nextOffset uint64, initialOrdinal uint64, subagentHistoryStartOrdinal *uint64, steps []rollout.ProjectionStep) (err error) {
	if startOffset > maxSQLiteInteger || nextOffset > maxSQLiteInteger || initialOrdinal > maxSQLiteInteger {
		return errors.New("thread history projection checkpoint exceeds SQLite integer range")
	}
	for _, step := range steps {
		if step.Ordinal > maxSQLiteInteger || step.EndOrdinalExclusive > maxSQLiteInteger || step.StartByteOffset > maxSQLiteInteger || step.EndByteOffset > maxSQLiteInteger {
			return errors.New("thread history projection step exceeds SQLite integer range")
		}
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin thread history projection: %w", err)
	}
	defer func() {
		if err != nil {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	expectedOffset, nextOrdinal := uint64(0), initialOrdinal
	var storedOffset, storedOrdinal int64
	queryErr := conn.QueryRowContext(ctx, `
SELECT next_rollout_byte_offset, next_rollout_ordinal
FROM thread_history_projection_state
WHERE thread_id = ?`, threadID).Scan(&storedOffset, &storedOrdinal)
	switch {
	case queryErr == nil:
		if storedOffset < 0 || storedOrdinal < 0 {
			return fmt.Errorf("thread history projection for %s has a negative checkpoint", threadID)
		}
		expectedOffset, nextOrdinal = uint64(storedOffset), uint64(storedOrdinal)
	case queryErr != sql.ErrNoRows:
		return fmt.Errorf("read transactional thread history projection: %w", queryErr)
	}
	if expectedOffset != startOffset {
		return fmt.Errorf("thread history projection for %s is behind durable rollout", threadID)
	}
	for _, step := range steps {
		switch step.Kind {
		case rollout.ProjectionSkippedOrdinalRange:
			if step.Ordinal != nextOrdinal || step.EndOrdinalExclusive <= step.Ordinal {
				return fmt.Errorf("thread history projection for %s has an invalid skipped ordinal range", threadID)
			}
			nextOrdinal = step.EndOrdinalExclusive
		case rollout.ProjectionLine:
			if step.Ordinal != nextOrdinal || step.Line == nil {
				return fmt.Errorf("thread history projection for %s expected ordinal %d, got %d", threadID, nextOrdinal, step.Ordinal)
			}
			changes := threadHistoryChanges{}
			if subagentHistoryStartOrdinal == nil || step.Ordinal >= *subagentHistoryStartOrdinal {
				changes, err = projectThreadHistoryLine(step.Line)
				if err != nil {
					return err
				}
			}
			if err := applyThreadHistoryChanges(ctx, conn, threadID, step, changes); err != nil {
				return err
			}
			if step.Ordinal == ^uint64(0) {
				return errors.New("rollout ordinal exceeds integer range")
			}
			nextOrdinal++
		default:
			return fmt.Errorf("unknown thread history projection step %q", step.Kind)
		}
	}
	if _, err := conn.ExecContext(ctx, `
INSERT INTO thread_history_projection_state (thread_id, next_rollout_byte_offset, next_rollout_ordinal)
VALUES (?, ?, ?)
ON CONFLICT(thread_id) DO UPDATE SET
    next_rollout_byte_offset = excluded.next_rollout_byte_offset,
    next_rollout_ordinal = excluded.next_rollout_ordinal`, threadID, sqliteInt(nextOffset), sqliteInt(nextOrdinal)); err != nil {
		return fmt.Errorf("advance thread history projection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit thread history projection: %w", err)
	}
	return nil
}

func projectThreadHistoryLine(line *rollout.Line) (threadHistoryChanges, error) {
	changes := threadHistoryChanges{}
	if line == nil || line.Type != "event_msg" || len(line.Payload) == 0 {
		return changes, nil
	}
	var event threadHistoryEvent
	if err := json.Unmarshal(line.Payload, &event); err != nil {
		return changes, err
	}
	turnID := firstHistoryString(event.TurnID, event.TurnIDCamel)
	switch normalizeThreadHistoryEventType(event.Type) {
	case "turn_started":
		if turnID == "" {
			return changes, errors.New("turn_started event is missing turn_id")
		}
		changes.Turns = append(changes.Turns, threadHistoryTurnChange{TurnID: turnID, Status: "inProgress", StartedAt: firstHistoryInt64(event.StartedAt, event.StartedAtCamel)})
	case "turn_complete":
		if turnID == "" {
			return changes, errors.New("turn_complete event is missing turn_id")
		}
		status := "completed"
		if rawJSONPresent(event.Error) {
			status = "failed"
		}
		changes.Turns = append(changes.Turns, threadHistoryTurnChange{
			TurnID: turnID, Status: status, Error: cloneRawJSON(event.Error),
			StartedAt: firstHistoryInt64(event.StartedAt, event.StartedAtCamel), CompletedAt: firstHistoryInt64(event.CompletedAt, event.CompletedAtCamel), DurationMS: firstHistoryInt64(event.DurationMS, event.DurationMSCamel),
		})
	case "turn_aborted":
		if turnID == "" {
			return changes, nil
		}
		changes.Turns = append(changes.Turns, threadHistoryTurnChange{
			TurnID: turnID, Status: "interrupted", StartedAt: firstHistoryInt64(event.StartedAt, event.StartedAtCamel), CompletedAt: firstHistoryInt64(event.CompletedAt, event.CompletedAtCamel), DurationMS: firstHistoryInt64(event.DurationMS, event.DurationMSCamel),
		})
	case "item_completed":
		if turnID == "" || !rawJSONPresent(event.Item) {
			return changes, errors.New("item_completed event is missing turn_id or item")
		}
		itemJSON, itemID, itemType, err := canonicalThreadItem(event.Item)
		if err != nil {
			return changes, err
		}
		startedAtMS := firstHistoryInt64(event.StartedAtMS, event.StartedAtMSCamel)
		if startedAtMS == nil {
			createdAt, err := time.Parse(time.RFC3339Nano, line.Timestamp)
			if err != nil {
				return changes, err
			}
			fallback := createdAt.UnixMilli()
			startedAtMS = &fallback
		}
		completedAtMS := firstHistoryInt64(event.CompletedAtMS, event.CompletedAtMSCamel)
		if completedAtMS != nil && *completedAtMS == 0 {
			completedAtMS = nil
		}
		changes.Items = append(changes.Items, threadHistoryItemChange{TurnID: turnID, ItemID: itemID, ItemType: itemType, ItemJSON: itemJSON, StartedAtMS: startedAtMS, CompletedAtMS: completedAtMS})
	}
	return changes, nil
}

func applyThreadHistoryChanges(ctx context.Context, conn *sql.Conn, threadID string, step rollout.ProjectionStep, changes threadHistoryChanges) error {
	ordinal, startOffset, endOffset := sqliteInt(step.Ordinal), sqliteInt(step.StartByteOffset), sqliteInt(step.EndByteOffset)
	for _, turn := range changes.Turns {
		var terminalOrdinal, terminalOffset any
		if turn.Status != "inProgress" {
			terminalOrdinal, terminalOffset = ordinal, endOffset
		}
		var errorJSON any
		if rawJSONPresent(turn.Error) {
			errorJSON = string(turn.Error)
		}
		if _, err := conn.ExecContext(ctx, `
INSERT INTO thread_turns (
    thread_id, turn_id, rollout_ordinal, rollout_byte_offset, rollout_end_ordinal,
    rollout_end_byte_offset, status, error_json, started_at, completed_at, duration_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(thread_id, turn_id) DO UPDATE SET
    rollout_end_ordinal = excluded.rollout_end_ordinal,
    rollout_end_byte_offset = excluded.rollout_end_byte_offset,
    status = excluded.status,
    error_json = excluded.error_json,
    started_at = excluded.started_at,
    completed_at = excluded.completed_at,
    duration_ms = excluded.duration_ms
WHERE thread_turns.rollout_end_ordinal IS NULL
  AND thread_turns.status = 'inProgress'`, threadID, turn.TurnID, ordinal, startOffset, terminalOrdinal, terminalOffset, turn.Status, errorJSON, nullableInt64(turn.StartedAt), nullableInt64(turn.CompletedAt), nullableInt64(turn.DurationMS)); err != nil {
			return fmt.Errorf("project thread turn: %w", err)
		}
		if err := refreshThreadSummaryIDs(ctx, conn, threadID, turn.TurnID, ordinal); err != nil {
			return err
		}
	}
	for _, item := range changes.Items {
		if item.StartedAtMS == nil {
			return fmt.Errorf("thread history projection for %s is missing an item creation timestamp", threadID)
		}
		if _, err := conn.ExecContext(ctx, `
INSERT INTO thread_items (
    thread_id, turn_id, item_id, rollout_ordinal, updated_at_ordinal,
    created_at_ms, item_type, item_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(thread_id, turn_id, item_id) DO UPDATE SET
    updated_at_ordinal = excluded.updated_at_ordinal,
    item_type = excluded.item_type,
    item_json = excluded.item_json`, threadID, item.TurnID, item.ItemID, ordinal, ordinal, *item.StartedAtMS, item.ItemType, string(item.ItemJSON)); err != nil {
			return fmt.Errorf("project thread item: %w", err)
		}
		switch item.ItemType {
		case "userMessage":
			_, err := conn.ExecContext(ctx, `
UPDATE thread_turns SET first_user_item_id = COALESCE(first_user_item_id, ?)
WHERE thread_id = ? AND turn_id = ? AND rollout_end_ordinal IS NULL AND status = 'inProgress'`, item.ItemID, threadID, item.TurnID)
			if err != nil {
				return fmt.Errorf("update first user item: %w", err)
			}
		case "agentMessage":
			var value map[string]any
			_ = json.Unmarshal(item.ItemJSON, &value)
			if phase, _ := value["phase"].(string); phase == "final_answer" {
				if _, err := conn.ExecContext(ctx, `
UPDATE thread_turns SET final_agent_item_id = ?
WHERE thread_id = ? AND turn_id = ? AND rollout_end_ordinal IS NULL AND status = 'inProgress'`, item.ItemID, threadID, item.TurnID); err != nil {
					return fmt.Errorf("update final agent item: %w", err)
				}
			}
		}
	}
	return nil
}

func refreshThreadSummaryIDs(ctx context.Context, conn *sql.Conn, threadID string, turnID string, ordinal int64) error {
	_, err := conn.ExecContext(ctx, `
UPDATE thread_turns
SET
    first_user_item_id = COALESCE(first_user_item_id, (
        SELECT item_id FROM thread_items
        WHERE thread_id = ? AND turn_id = ?
          AND (item_type = 'userMessage' OR (item_type = '' AND json_extract(item_json, '$.type') = 'userMessage'))
        ORDER BY rollout_ordinal LIMIT 1
    )),
    final_agent_item_id = COALESCE((
        SELECT item_id FROM thread_items
        WHERE thread_id = ? AND turn_id = ?
          AND (item_type = 'agentMessage' OR (item_type = '' AND json_extract(item_json, '$.type') = 'agentMessage'))
          AND json_extract(item_json, '$.phase') = 'final_answer'
        ORDER BY rollout_ordinal DESC LIMIT 1
    ), CASE WHEN status IN ('completed', 'interrupted', 'failed') THEN (
        SELECT item_id FROM thread_items
        WHERE thread_id = ? AND turn_id = ?
          AND (item_type = 'agentMessage' OR (item_type = '' AND json_extract(item_json, '$.type') = 'agentMessage'))
          AND json_extract(item_json, '$.phase') IS NULL
        ORDER BY rollout_ordinal DESC LIMIT 1
    ) END, final_agent_item_id)
WHERE thread_id = ? AND turn_id = ? AND (rollout_end_ordinal = ? OR status = 'inProgress')`, threadID, turnID, threadID, turnID, threadID, turnID, threadID, turnID, ordinal)
	if err != nil {
		return fmt.Errorf("refresh thread summary items: %w", err)
	}
	return nil
}

func canonicalThreadItem(raw json.RawMessage) (json.RawMessage, string, string, error) {
	return rollout.PublicThreadItemJSONFromCore(raw)
}

func normalizeThreadItemType(kind string) string {
	compact := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(kind)))
	switch compact {
	case "usermessage":
		return "userMessage"
	case "agentmessage", "assistantmessage":
		return "agentMessage"
	case "commandexecution":
		return "commandExecution"
	case "filechange":
		return "fileChange"
	case "mcptoolcall":
		return "mcpToolCall"
	case "dynamictoolcall":
		return "dynamicToolCall"
	case "collabagenttoolcall":
		return "collabAgentToolCall"
	case "subagentactivity":
		return "subAgentActivity"
	case "hookprompt":
		return "hookPrompt"
	case "plan":
		return "plan"
	case "reasoning":
		return "reasoning"
	default:
		if strings.TrimSpace(kind) != "" {
			return kind
		}
		return ""
	}
}

func normalizeThreadHistoryEventType(kind string) string {
	compact := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(kind)))
	switch compact {
	case "taskstarted", "turnstarted":
		return "turn_started"
	case "taskcomplete", "turncomplete":
		return "turn_complete"
	case "turnaborted":
		return "turn_aborted"
	case "itemcompleted":
		return "item_completed"
	default:
		return ""
	}
}

func firstHistoryString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstHistoryInt64(values ...*int64) *int64 {
	for _, value := range values {
		if value != nil {
			copy := *value
			return &copy
		}
	}
	return nil
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
func sqliteInt(value uint64) int64 { return int64(value) }
func cloneRawJSON(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}
func rawJSONPresent(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}
