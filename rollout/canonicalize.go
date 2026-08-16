package rollout

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex_go/session"
)

// MigrationJournalDirectory mirrors Rust publish.rs
// MIGRATION_JOURNAL_DIRECTORY ("rollout-migrations").
const MigrationJournalDirectory = "rollout-migrations"

// CanonicalizeRollout converts one legacy rollout into staged paginated JSONL,
// mirroring Rust LegacyRolloutCanonicalizer + publish helpers:
//
//   - reads the source rollout (plain or .jsonl.zst),
//   - replays legacy events/items into canonical session items,
//   - writes a staged paginated rollout at ".{name}.paginated.tmp",
//   - writes the durable ".pending" journal under rollout-migrations/,
//   - atomically renames the staged file over the source,
//   - removes the journal only after the publish is durable.
//
// On any failure the source rollout is left untouched; a leftover staged file
// is removed so a later run can retry.
func CanonicalizeRollout(codexHome string, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("rollout path is required")
	}
	source, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open source rollout: %w", err)
	}
	sourceInfo, statErr := source.Stat()
	if statErr != nil {
		source.Close()
		return fmt.Errorf("stat source rollout: %w", statErr)
	}
	source.Close()
	if sourceInfo.Size() == 0 {
		return errors.New("rollout is empty; nothing to migrate")
	}

	lines, _, err := Load(path)
	if err != nil {
		return fmt.Errorf("load legacy rollout: %w", err)
	}
	meta := firstSessionMetaFromLines(lines)
	if meta == nil {
		return fmt.Errorf("rollout contains no session metadata: %s", path)
	}
	if strings.EqualFold(strings.TrimSpace(meta.HistoryMode), "paginated") {
		return errors.New("rollout is already paginated")
	}
	createdAt := parseMetaTimestamp(meta.Timestamp)
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	items, turns := sessionItemsFromRolloutLines(lines, createdAt)

	stagedPath, err := stagedRolloutPath(path)
	if err != nil {
		return err
	}
	journalPath, err := migrationJournalPath(codexHome, strings.TrimSpace(meta.ID))
	if err != nil {
		return err
	}
	_ = os.Remove(stagedPath)
	file, err := os.OpenFile(stagedPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create staged rollout: %w", err)
	}
	writeErr := writeCanonicalPaginated(file, meta, items, turns, createdAt)
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(stagedPath)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(stagedPath)
		return fmt.Errorf("close staged rollout: %w", closeErr)
	}

	if err := writeMigrationJournal(journalPath); err != nil {
		_ = os.Remove(stagedPath)
		return err
	}
	if err := os.Rename(stagedPath, path); err != nil {
		_ = os.Remove(stagedPath)
		return fmt.Errorf("publish staged rollout: %w", err)
	}
	if err := os.Remove(journalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove migration journal: %w", err)
	}
	return nil
}

func firstSessionMetaFromLines(lines []Line) *SessionMeta {
	for i := range lines {
		if lines[i].Meta != nil {
			return lines[i].Meta
		}
	}
	return nil
}

// stagedRolloutPath mirrors Rust staged_rollout_path: the staged file sits next
// to the source as ".{name}.paginated.tmp".
func stagedRolloutPath(rolloutPath string) (string, error) {
	name := filepath.Base(rolloutPath)
	if strings.TrimSpace(name) == "" {
		return "", errors.New("rollout path has no valid filename")
	}
	return filepath.Join(filepath.Dir(rolloutPath), "."+name+".paginated.tmp"), nil
}

// migrationJournalPath mirrors Rust migration_journal_path.
func migrationJournalPath(codexHome string, threadID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", errors.New("rollout thread id is required for the migration journal")
	}
	return filepath.Join(codexHome, MigrationJournalDirectory, threadID+".pending"), nil
}

// writeMigrationJournal durably marks a thread as pending migration.
func writeMigrationJournal(journalPath string) error {
	parent := filepath.Dir(journalPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create migration journal directory: %w", err)
	}
	file, err := os.OpenFile(journalPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create migration journal: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync migration journal: %w", err)
	}
	return file.Close()
}

// writeCanonicalPaginated writes the canonical paginated rollout: session_meta
// header (history_mode=paginated, no history_base), then per-turn event
// boundaries and completed items in ordinal order.
func writeCanonicalPaginated(file *os.File, meta *SessionMeta, items []session.Item, turns []session.TurnSnapshot, createdAt time.Time) error {
	canonical := *meta
	canonical.HistoryMode = "paginated"
	canonical.HistoryBase = nil
	canonical.SubagentHistoryStartOrdinal = nil
	ordinal := uint64(0)
	head, err := json.Marshal(Line{
		Type:      "session_meta",
		Timestamp: createdAt.UTC().Format(time.RFC3339Nano),
		Meta:      &canonical,
		Ordinal:   &ordinal,
	})
	if err != nil {
		return fmt.Errorf("marshal canonical session meta: %w", err)
	}
	if _, err := file.Write(append(head, '\n')); err != nil {
		return fmt.Errorf("write canonical session meta: %w", err)
	}
	ordinal++

	turnByID := map[string]session.TurnSnapshot{}
	for i := range turns {
		turnByID[turns[i].ID] = turns[i]
	}
	writtenTurns := map[string]bool{}
	for i := range items {
		item := &items[i]
		turnID := sessionItemTurnID(item, i)
		if turnID != "" && !writtenTurns[turnID] {
			if turn, ok := turnByID[turnID]; ok {
				startedAt := timeFromUnixOrZero(turn.StartedAt)
				if err := writeCanonicalEventLine(file, "task_started", turnID, startedAt, ordinal, map[string]any{"started_at": turn.StartedAt}); err != nil {
					return err
				}
				ordinal++
			} else {
				if err := writeCanonicalEventLine(file, "task_started", turnID, time.Time{}, ordinal, map[string]any{"started_at": nil}); err != nil {
					return err
				}
				ordinal++
			}
			writtenTurns[turnID] = true
		}
		raw, _, err := CoreTurnItemJSONFromSessionItem(item)
		if err != nil || len(raw) == 0 {
			continue
		}
		completedAt := item.CreatedAt
		if completedAt.IsZero() {
			completedAt = createdAt
		}
		if err := writeCanonicalCompletedItem(file, raw, turnID, completedAt, ordinal); err != nil {
			return err
		}
		ordinal++
	}
	for i := range turns {
		turn := turns[i]
		if turn.Status != "completed" && turn.Status != "" {
			continue
		}
		payload := map[string]any{"completed_at": turn.CompletedAt}
		if turn.CompletedAt != nil {
			payload["duration_ms"] = turn.DurationMS
		}
		completedAt := timeFromUnixOrZero(turn.CompletedAt)
		if err := writeCanonicalEventLine(file, "task_complete", turn.ID, completedAt, ordinal, payload); err != nil {
			return err
		}
		ordinal++
	}
	return nil
}

func writeCanonicalEventLine(file *os.File, eventType string, turnID string, eventTime time.Time, ordinal uint64, fields map[string]any) error {
	payload := map[string]any{"type": eventType, "turn_id": turnID}
	for key, value := range fields {
		payload[key] = value
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal canonical event %s: %w", eventType, err)
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	if !eventTime.IsZero() {
		timestamp = eventTime.UTC().Format(time.RFC3339Nano)
	}
	line, err := json.Marshal(Line{Type: "event_msg", Timestamp: timestamp, Payload: data, Ordinal: &ordinal})
	if err != nil {
		return fmt.Errorf("marshal canonical event line: %w", err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write canonical event %s: %w", eventType, err)
	}
	return nil
}

func writeCanonicalCompletedItem(file *os.File, raw json.RawMessage, turnID string, completedAt time.Time, ordinal uint64) error {
	// Mirrors Rust canonicalizer write_completed_item_to_turn: the canonical
	// paginated line is an event_msg whose payload is an ItemCompleted event
	// wrapping the TurnItem under the `item` key.
	completedAtMS := completedAt.UTC().UnixMilli()
	payload := map[string]any{
		"type":            "item_completed",
		"turn_id":         turnID,
		"completed_at_ms": completedAtMS,
		"item":            json.RawMessage(raw),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal canonical item_completed payload: %w", err)
	}
	line, err := json.Marshal(Line{
		Type:      "event_msg",
		Timestamp: completedAt.UTC().Format(time.RFC3339Nano),
		Payload:   data,
		Ordinal:   &ordinal,
	})
	if err != nil {
		return fmt.Errorf("marshal canonical completed item: %w", err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write canonical completed item: %w", err)
	}
	return nil
}

func timeFromUnixOrZero(value *int64) time.Time {
	if value == nil {
		return time.Time{}
	}
	return time.Unix(*value, 0).UTC()
}
