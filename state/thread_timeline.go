package state

// Canonical thread timeline reads: ordinary items, realtime items, and turn
// boundaries interleaved in rollout order (Rust #40508 `thread/timeline/list`).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// ThreadTimelineEntryKind identifies the entry type. The numeric order breaks
// ties at one rollout ordinal: turn start, item, realtime item, turn end.
const (
	ThreadTimelineTurnStarted   = 0
	ThreadTimelineItem          = 1
	ThreadTimelineRealtimeItem  = 2
	ThreadTimelineTurnCompleted = 3
)

// ThreadTimelineListParams requests one canonical timeline page. Pages always
// walk newest to oldest; the response is reversed into rollout order.
type ThreadTimelineListParams struct {
	ThreadID string
	Cursor   *string
	PageSize int
}

// ThreadTimelineEntry is one item or turn boundary in canonical rollout order.
type ThreadTimelineEntry struct {
	Kind     int
	Position uint64
	TurnID   string
	// ID is the item or turn identifier used for stable page ordering.
	ID string

	// Item fields (Kind == ThreadTimelineItem).
	ItemJSON      json.RawMessage
	StartedAtMS   *int64
	CompletedAtMS *int64

	// Realtime fields (Kind == ThreadTimelineRealtimeItem).
	RealtimeItemJSON json.RawMessage

	// Turn boundary fields.
	Status      string
	ErrorJSON   json.RawMessage
	StartedAt   *int64
	CompletedAt *int64
	DurationMS  *int64
}

// ThreadTimelinePage is a bounded timeline page in rollout order.
type ThreadTimelinePage struct {
	Entries                          []ThreadTimelineEntry
	NextCursor                       *string
	ActiveRealtimeSessionAtPageStart *string
}

type threadTimelineCursor struct {
	ThreadID string `json:"threadId"`
	Position uint64 `json:"position"`
	Kind     uint8  `json:"kind"`
	ID       string `json:"id"`
}

// timelineEntryKey returns the stable (position, kind, id) ordering key.
func timelineEntryKey(entry *ThreadTimelineEntry) (uint64, int, string) {
	switch entry.Kind {
	case ThreadTimelineTurnStarted, ThreadTimelineTurnCompleted:
		return entry.Position, entry.Kind, entry.TurnID
	default:
		return entry.Position, entry.Kind, entry.ID
	}
}

// ListThreadTimeline pages the canonical timeline of a paginated thread.
func (r *StateRuntime) ListThreadTimeline(ctx context.Context, params ThreadTimelineListParams) (*ThreadTimelinePage, error) {
	if params.PageSize <= 0 {
		return nil, invalidThreadHistory("page size must be positive")
	}
	lineage, db, err := r.prepareThreadHistoryPageRead(ctx, params.ThreadID, "thread/timeline/list")
	if err != nil {
		return nil, err
	}
	cursor, err := parseThreadTimelineCursor(params.ThreadID, params.Cursor)
	if err != nil {
		return nil, err
	}
	cursorPosition, cursorKind, cursorID := uint64(0), 4, ""
	if cursor != nil {
		cursorPosition, cursorKind, cursorID = cursor.Position, int(cursor.Kind), cursor.ID
	}
	cursorOrdinal := int64(math.MaxInt64)
	if cursor != nil {
		cursorOrdinal = sqliteInt(cursor.Position)
	}

	maxRows := params.PageSize + 1
	entries := make([]ThreadTimelineEntry, 0, maxRows)
	for i := len(lineage) - 1; i >= 0 && len(entries) < maxRows; i-- {
		segment := lineage[i]
		if cursor != nil && cursorPosition < segment.Start {
			continue
		}
		upper := uint64(math.MaxInt64)
		if segment.End != nil && segment.End.EndOrdinalExclusive > 0 {
			upper = segment.End.EndOrdinalExclusive
		}
		rows, err := readThreadTimelineSegment(ctx, db, segment, upper, cursorOrdinal, cursorKind, cursorID, maxRows-len(entries))
		if err != nil {
			return nil, err
		}
		entries = append(entries, rows...)
	}

	hasMore := len(entries) > params.PageSize
	if hasMore {
		entries = entries[:params.PageSize]
	}
	var nextCursor *string
	if hasMore && len(entries) > 0 {
		position, kind, id := timelineEntryKey(&entries[len(entries)-1])
		encoded, err := json.Marshal(threadTimelineCursor{
			ThreadID: strings.TrimSpace(params.ThreadID),
			Position: position,
			Kind:     uint8(kind),
			ID:       id,
		})
		if err != nil {
			return nil, fmt.Errorf("encode thread timeline cursor: %w", err)
		}
		value := string(encoded)
		nextCursor = &value
	}

	pageStartPosition, pageStartKind, pageStartID := uint64(0), 4, ""
	if len(entries) > 0 {
		pageStartPosition, pageStartKind, pageStartID = timelineEntryKey(&entries[len(entries)-1])
	}
	sessionAtPageStart, err := activeRealtimeSessionBefore(ctx, db, lineage, pageStartPosition, pageStartKind, pageStartID)
	if err != nil {
		return nil, err
	}

	// Collected newest-first; the public page is oldest-first.
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
	return &ThreadTimelinePage{
		Entries:                          entries,
		NextCursor:                       nextCursor,
		ActiveRealtimeSessionAtPageStart: sessionAtPageStart,
	}, nil
}

func parseThreadTimelineCursor(threadID string, raw *string) (*threadTimelineCursor, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	var cursor threadTimelineCursor
	if err := json.Unmarshal([]byte(*raw), &cursor); err != nil {
		return nil, invalidThreadHistory("invalid thread timeline cursor")
	}
	if cursor.ThreadID != strings.TrimSpace(threadID) {
		return nil, invalidThreadHistory("thread timeline cursor belongs to another thread")
	}
	return &cursor, nil
}

// readThreadTimelineSegment reads one rollout lineage segment's rows older
// than the cursor, newest first.
func readThreadTimelineSegment(ctx context.Context, db *sql.DB, segment threadHistoryLineageSegment, upper uint64, cursorOrdinal int64, cursorKind int, cursorID string, limit int) ([]ThreadTimelineEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	start := sqliteInt(segment.Start)
	upperInt := sqliteInt(upper)
	entries := make([]ThreadTimelineEntry, 0, limit)

	turnStarts, err := db.QueryContext(ctx, `
SELECT rollout_ordinal, turn_id, started_at
FROM thread_turns
WHERE thread_id = ? AND rollout_ordinal >= ? AND rollout_ordinal < ?
  AND rollout_ordinal <= ?
  AND (rollout_ordinal, 0, turn_id) < (?, ?, ?)
ORDER BY rollout_ordinal DESC, turn_id DESC
LIMIT ?`, segment.ThreadID, start, upperInt, cursorOrdinal, cursorOrdinal, 0, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("read thread timeline turn starts: %w", err)
	}
	for turnStarts.Next() {
		var entry ThreadTimelineEntry
		var position int64
		if err := turnStarts.Scan(&position, &entry.TurnID, &entry.StartedAt); err != nil {
			_ = turnStarts.Close()
			return nil, fmt.Errorf("scan thread timeline turn start: %w", err)
		}
		entry.Kind = ThreadTimelineTurnStarted
		entry.Position = uint64(position)
		entries = append(entries, entry)
	}
	if err := turnStarts.Err(); err != nil {
		_ = turnStarts.Close()
		return nil, fmt.Errorf("read thread timeline turn starts: %w", err)
	}
	_ = turnStarts.Close()

	items, err := db.QueryContext(ctx, `
SELECT turn_id, item_id, rollout_ordinal, started_at_ms, completed_at_ms, item_json
FROM thread_items
WHERE thread_id = ? AND rollout_ordinal >= ? AND rollout_ordinal < ?
  AND rollout_ordinal <= ?
  AND (rollout_ordinal, 1, item_id) < (?, ?, ?)
ORDER BY rollout_ordinal DESC, item_id DESC
LIMIT ?`, segment.ThreadID, start, upperInt, cursorOrdinal, cursorOrdinal, 1, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("read thread timeline items: %w", err)
	}
	for items.Next() {
		var entry ThreadTimelineEntry
		var position int64
		var itemJSON string
		if err := items.Scan(&entry.TurnID, &entry.ID, &position, &entry.StartedAtMS, &entry.CompletedAtMS, &itemJSON); err != nil {
			_ = items.Close()
			return nil, fmt.Errorf("scan thread timeline item: %w", err)
		}
		entry.Kind = ThreadTimelineItem
		entry.Position = uint64(position)
		entry.ItemJSON = json.RawMessage(itemJSON)
		entries = append(entries, entry)
	}
	if err := items.Err(); err != nil {
		_ = items.Close()
		return nil, fmt.Errorf("read thread timeline items: %w", err)
	}
	_ = items.Close()

	realtimeItems, err := db.QueryContext(ctx, `
SELECT item_id, rollout_ordinal, item_json
FROM thread_realtime_items
WHERE thread_id = ? AND rollout_ordinal >= ? AND rollout_ordinal < ?
  AND rollout_ordinal <= ?
  AND (rollout_ordinal, 2, item_id) < (?, ?, ?)
ORDER BY rollout_ordinal DESC, item_id DESC
LIMIT ?`, segment.ThreadID, start, upperInt, cursorOrdinal, cursorOrdinal, 2, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("read thread timeline realtime items: %w", err)
	}
	for realtimeItems.Next() {
		var entry ThreadTimelineEntry
		var position int64
		var itemJSON string
		if err := realtimeItems.Scan(&entry.ID, &position, &itemJSON); err != nil {
			_ = realtimeItems.Close()
			return nil, fmt.Errorf("scan thread timeline realtime item: %w", err)
		}
		entry.Kind = ThreadTimelineRealtimeItem
		entry.Position = uint64(position)
		entry.RealtimeItemJSON = json.RawMessage(itemJSON)
		entries = append(entries, entry)
	}
	if err := realtimeItems.Err(); err != nil {
		_ = realtimeItems.Close()
		return nil, fmt.Errorf("read thread timeline realtime items: %w", err)
	}
	_ = realtimeItems.Close()

	turnEnds, err := db.QueryContext(ctx, `
SELECT rollout_end_ordinal, turn_id, status, error_json, started_at, completed_at, duration_ms
FROM thread_turns
WHERE thread_id = ? AND rollout_end_ordinal IS NOT NULL
  AND rollout_end_ordinal >= ? AND rollout_end_ordinal < ?
  AND rollout_end_ordinal <= ?
  AND (rollout_end_ordinal, 3, turn_id) < (?, ?, ?)
ORDER BY rollout_end_ordinal DESC, turn_id DESC
LIMIT ?`, segment.ThreadID, start, upperInt, cursorOrdinal, cursorOrdinal, 3, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("read thread timeline turn ends: %w", err)
	}
	for turnEnds.Next() {
		var entry ThreadTimelineEntry
		var position int64
		var errorJSON sql.NullString
		if err := turnEnds.Scan(&position, &entry.TurnID, &entry.Status, &errorJSON, &entry.StartedAt, &entry.CompletedAt, &entry.DurationMS); err != nil {
			_ = turnEnds.Close()
			return nil, fmt.Errorf("scan thread timeline turn end: %w", err)
		}
		entry.Kind = ThreadTimelineTurnCompleted
		entry.Position = uint64(position)
		if errorJSON.Valid && strings.TrimSpace(errorJSON.String) != "" && errorJSON.String != "null" {
			entry.ErrorJSON = json.RawMessage(errorJSON.String)
		}
		entries = append(entries, entry)
	}
	if err := turnEnds.Err(); err != nil {
		_ = turnEnds.Close()
		return nil, fmt.Errorf("read thread timeline turn ends: %w", err)
	}
	_ = turnEnds.Close()

	sort.SliceStable(entries, func(i, j int) bool {
		leftPosition, leftKind, leftID := timelineEntryKey(&entries[i])
		rightPosition, rightKind, rightID := timelineEntryKey(&entries[j])
		switch {
		case leftPosition != rightPosition:
			return leftPosition > rightPosition
		case leftKind != rightKind:
			return leftKind > rightKind
		default:
			return leftID > rightID
		}
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// activeRealtimeSessionBefore resolves the session that was open immediately
// before the page's oldest entry.
func activeRealtimeSessionBefore(ctx context.Context, db *sql.DB, lineage []threadHistoryLineageSegment, position uint64, kind int, id string) (*string, error) {
	for i := len(lineage) - 1; i >= 0; i-- {
		segment := lineage[i]
		upper := position
		if segment.End != nil && segment.End.EndOrdinalExclusive > 0 && segment.End.EndOrdinalExclusive-1 < upper {
			upper = segment.End.EndOrdinalExclusive - 1
		}
		if upper < segment.Start {
			continue
		}
		var itemType string
		var sessionID sql.NullString
		err := db.QueryRowContext(ctx, `
SELECT item_type, json_extract(item_json, '$.realtime_session_id')
FROM thread_realtime_items
WHERE thread_id = ?
  AND rollout_ordinal >= ?
  AND rollout_ordinal <= ?
  AND (rollout_ordinal, 2, item_id) < (?, ?, ?)
  AND item_type IN ('realtime_session_started', 'realtime_session_closed')
ORDER BY rollout_ordinal DESC
LIMIT 1`, segment.ThreadID, sqliteInt(segment.Start), sqliteInt(upper), sqliteInt(position), kind, id).Scan(&itemType, &sessionID)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read realtime session at timeline page start: %w", err)
		}
		if itemType == "realtime_session_started" && sessionID.Valid && strings.TrimSpace(sessionID.String) != "" {
			value := sessionID.String
			return &value, nil
		}
		return nil, nil
	}
	return nil, nil
}
