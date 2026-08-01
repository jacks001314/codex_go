package rollout

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"codex_go/session"
)

// ResolveHistoryPrefix reconstructs the model-visible history frozen by a
// paginated rollout reference. Byte offsets are coordinates in the
// uncompressed JSONL stream, matching Rust's HistoryPosition contract.
func ResolveHistoryPrefix(codexHome string, position HistoryPosition) ([]session.Item, []session.TurnSnapshot, error) {
	return resolveHistoryPrefix(codexHome, position, map[string]struct{}{})
}

// RecordFromPathResolved reads a rollout and recursively materializes its
// reference-backed inherited prefix for model context and Go store operations.
func RecordFromPathResolved(codexHome string, path string, archived bool) (*session.Record, error) {
	record, err := RecordFromPath(path, archived)
	if err != nil || record.HistoryBase == nil {
		return record, err
	}
	visiting := map[string]struct{}{string(record.ID): {}}
	items, turns, err := resolveHistoryPrefix(codexHome, historyPositionFromSession(record.HistoryBase), visiting)
	if err != nil {
		return nil, fmt.Errorf("resolve history base for thread %s: %w", record.ID, err)
	}
	record.InheritedItems = len(items)
	record.InheritedTurns = len(turns)
	record.Items = append(items, record.Items...)
	record.Metadata.RolloutTurns = append(turns, record.Metadata.RolloutTurns...)
	if preview := previewFromSessionItems(record.Items); preview != "" {
		record.Preview = preview
	}
	if len(record.Items) > 0 {
		last := record.Items[len(record.Items)-1].CreatedAt
		if !last.IsZero() && last.After(record.RecencyAt) {
			record.RecencyAt = last
		}
	}
	return record, nil
}

func resolveHistoryPrefix(codexHome string, position HistoryPosition, visiting map[string]struct{}) ([]session.Item, []session.TurnSnapshot, error) {
	threadID := strings.TrimSpace(position.ThreadID)
	if threadID == "" {
		return nil, nil, errors.New("history position thread id is required")
	}
	if _, exists := visiting[threadID]; exists {
		return nil, nil, fmt.Errorf("cyclic history reference at thread %s", threadID)
	}
	visiting[threadID] = struct{}{}
	defer delete(visiting, threadID)

	path, err := findThreadPathIncludingArchived(codexHome, threadID)
	if err != nil {
		return nil, nil, err
	}
	meta, err := FirstSessionMeta(path)
	if err != nil {
		return nil, nil, err
	}
	var items []session.Item
	var turns []session.TurnSnapshot
	if meta.HistoryBase != nil {
		items, turns, err = resolveHistoryPrefix(codexHome, *meta.HistoryBase, visiting)
		if err != nil {
			return nil, nil, err
		}
	}
	lines, err := readProjectionPrefixLines(path, position.EndByteOffset, position.EndOrdinalExclusive)
	if err != nil {
		return nil, nil, fmt.Errorf("read history prefix for thread %s: %w", threadID, err)
	}
	fallback := parseMetaTimestamp(meta.Timestamp)
	if fallback.IsZero() {
		fallback = time.Unix(0, 0).UTC()
	}
	localItems, localTurns := sessionItemsFromRolloutLines(lines, fallback)
	items = append(items, localItems...)
	turns = append(turns, localTurns...)
	return items, turns, nil
}

func readProjectionPrefixLines(path string, endByteOffset uint64, endOrdinalExclusive uint64) ([]Line, error) {
	if endByteOffset == 0 && endOrdinalExclusive == 0 {
		return nil, nil
	}
	data, fileEnd, err := readProjectionSuffix(path, 0)
	if err != nil {
		return nil, err
	}
	if endByteOffset > fileEnd || endByteOffset > uint64(len(data)) {
		return nil, errors.New("history byte boundary exceeds durable rollout")
	}
	prefix := data[:int(endByteOffset)]
	if len(prefix) == 0 || prefix[len(prefix)-1] != '\n' {
		return nil, errors.New("history byte boundary is not a complete JSONL record")
	}
	steps, nextOffset, err := readProjectionStepsData(prefix, 0, 0, nil)
	if err != nil {
		return nil, err
	}
	if nextOffset != endByteOffset {
		return nil, errors.New("history byte boundary contains an unrecognized rollout record")
	}
	nextOrdinal := uint64(0)
	lines := make([]Line, 0, len(steps))
	for _, step := range steps {
		switch step.Kind {
		case ProjectionSkippedOrdinalRange:
			nextOrdinal = step.EndOrdinalExclusive
		case ProjectionLine:
			if step.Line == nil || step.Ordinal == ^uint64(0) {
				return nil, errors.New("invalid rollout projection line")
			}
			lines = append(lines, *step.Line)
			nextOrdinal = step.Ordinal + 1
		}
	}
	if nextOrdinal != endOrdinalExclusive {
		return nil, fmt.Errorf("history ordinal boundary mismatch: got %d, want %d", nextOrdinal, endOrdinalExclusive)
	}
	return lines, nil
}

func findThreadPathIncludingArchived(codexHome string, threadID string) (string, error) {
	active, activeErr := FindThreadPath(codexHome, threadID, false)
	if activeErr == nil {
		return active, nil
	}
	archived, archivedErr := FindThreadPath(codexHome, threadID, true)
	if archivedErr == nil {
		return archived, nil
	}
	return "", fmt.Errorf("rollout thread not found: %s", threadID)
}

func historyPositionFromSession(position *session.HistoryPosition) HistoryPosition {
	if position == nil {
		return HistoryPosition{}
	}
	return HistoryPosition{
		ThreadID:            string(position.ThreadID),
		EndOrdinalExclusive: position.EndOrdinalExclusive,
		EndByteOffset:       position.EndByteOffset,
	}
}
