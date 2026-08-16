package rollout

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

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

	compressed := strings.HasSuffix(strings.ToLower(path), ".jsonl.zst")
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
	finalOrdinal, writeErr := writeCanonicalPaginated(file, meta, items, turns, createdAt)
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(stagedPath)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(stagedPath)
		return fmt.Errorf("close staged rollout: %w", closeErr)
	}
	if isSubagentRollout(meta) {
		// Mirrors Rust rewrite_subagent_history_boundary: a migrated subagent
		// rollout marks the ordinal from which its own history starts so
		// SQLite projection and resume skip the copied parent history prefix.
		if err := rewriteStagedSubagentBoundary(stagedPath, finalOrdinal); err != nil {
			_ = os.Remove(stagedPath)
			return err
		}
	}

	if err := writeMigrationJournal(journalPath); err != nil {
		_ = os.Remove(stagedPath)
		return err
	}
	if compressed {
		// Mirrors Rust compress_rollout_to_path + publish: the staged plain
		// JSONL is compressed to .{name}.paginated.zst.tmp and that artifact is
		// renamed over the compressed source.
		compressedStagedPath, err := stagedRolloutPathWithSuffix(path, "paginated.zst")
		if err != nil {
			_ = os.Remove(stagedPath)
			return err
		}
		if err := compressRolloutToPath(stagedPath, compressedStagedPath); err != nil {
			_ = os.Remove(stagedPath)
			return err
		}
		_ = os.Remove(stagedPath)
		if err := os.Rename(compressedStagedPath, path); err != nil {
			_ = os.Remove(compressedStagedPath)
			return fmt.Errorf("publish compressed staged rollout: %w", err)
		}
	} else {
		if err := os.Rename(stagedPath, path); err != nil {
			_ = os.Remove(stagedPath)
			return fmt.Errorf("publish staged rollout: %w", err)
		}
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
	return stagedRolloutPathWithSuffix(rolloutPath, "paginated")
}

func stagedRolloutPathWithSuffix(rolloutPath string, suffix string) (string, error) {
	name := filepath.Base(rolloutPath)
	if strings.TrimSpace(name) == "" {
		return "", errors.New("rollout path has no valid filename")
	}
	return filepath.Join(filepath.Dir(rolloutPath), "."+name+"."+suffix+".tmp"), nil
}

func compressRolloutToPath(plainPath string, compressedPath string) error {
	data, err := os.ReadFile(plainPath)
	if err != nil {
		return fmt.Errorf("read staged rollout for compression: %w", err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		return fmt.Errorf("create zstd encoder: %w", err)
	}
	defer encoder.Close()
	encoded := encoder.EncodeAll(data, nil)
	if err := os.WriteFile(compressedPath, encoded, 0o600); err != nil {
		return fmt.Errorf("write compressed staged rollout: %w", err)
	}
	return nil
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
func writeCanonicalPaginated(file *os.File, meta *SessionMeta, items []session.Item, turns []session.TurnSnapshot, createdAt time.Time) (uint64, error) {
	canonical := *meta
	canonical.HistoryMode = "paginated"
	canonical.HistoryBase = nil
	ordinal := uint64(0)
	head, err := json.Marshal(Line{
		Type:      "session_meta",
		Timestamp: createdAt.UTC().Format(time.RFC3339Nano),
		Meta:      &canonical,
		Ordinal:   &ordinal,
	})
	if err != nil {
		return 0, fmt.Errorf("marshal canonical session meta: %w", err)
	}
	if _, err := file.Write(append(head, '\n')); err != nil {
		return 0, fmt.Errorf("write canonical session meta: %w", err)
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
					return 0, err
				}
				ordinal++
			} else {
				if err := writeCanonicalEventLine(file, "task_started", turnID, time.Time{}, ordinal, map[string]any{"started_at": nil}); err != nil {
					return 0, err
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
			return 0, err
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
			return 0, err
		}
		ordinal++
	}
	return ordinal, nil
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

// isSubagentRollout mirrors Rust migrate_rollout_path's RolloutMigrationKind
// detection: a non-memory-consolidation session whose source is a subagent or
// whose thread_source is "subagent" is treated as a subagent rollout.
func isSubagentRollout(meta *SessionMeta) bool {
	if meta == nil {
		return false
	}
	source := strings.ToLower(strings.TrimSpace(meta.Source))
	threadSource := strings.ToLower(strings.TrimSpace(meta.ThreadSource))
	if source == "internal_memory_consolidation" || threadSource == "memory_consolidation" {
		return false
	}
	if strings.HasPrefix(source, "subagent") || strings.Contains(source, "sub_agent") {
		return true
	}
	if threadSource == "subagent" || threadSource == "subagentreview" || threadSource == "subagentcompact" || threadSource == "subagentthreadspawn" || threadSource == "subagentother" {
		return true
	}
	return false
}

// rewriteStagedSubagentBoundary mirrors Rust rewrite_subagent_history_boundary:
// it rewrites the staged rollout's head session_meta to carry the ordinal from
// which the subagent's own history starts (the canonical writer's final
// ordinal, i.e. the first ordinal after all copied parent history).
func rewriteStagedSubagentBoundary(stagedPath string, boundary uint64) error {
	data, err := os.ReadFile(stagedPath)
	if err != nil {
		return fmt.Errorf("read staged rollout for subagent boundary: %w", err)
	}
	lines := strings.SplitN(string(data), "\n", 2)
	if len(lines) == 0 {
		return errors.New("staged rollout is empty")
	}
	var head Line
	if err := json.Unmarshal([]byte(lines[0]), &head); err != nil {
		return fmt.Errorf("parse staged head: %w", err)
	}
	// The staged head is serialized by Line.MarshalJSON: session_meta lines
	// carry the SessionMeta under the "payload" key.
	if head.Meta == nil && len(head.Payload) == 0 {
		return errors.New("staged rollout head is not session metadata")
	}
	meta := head.Meta
	if meta == nil {
		var parsed SessionMeta
		if err := json.Unmarshal(head.Payload, &parsed); err != nil {
			return fmt.Errorf("parse staged head session metadata: %w", err)
		}
		meta = &parsed
	}
	meta.SubagentHistoryStartOrdinal = &boundary
	head.Meta = meta
	encoded, err := json.Marshal(head)
	if err != nil {
		return fmt.Errorf("marshal rewritten head: %w", err)
	}
	rewritten := string(encoded)
	if len(lines) == 2 {
		rewritten += "\n" + lines[1]
	}
	if err := os.WriteFile(stagedPath, []byte(rewritten), 0o600); err != nil {
		return fmt.Errorf("rewrite staged subagent boundary: %w", err)
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
