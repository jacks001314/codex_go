package state

// Snapshot reads for persisted realtime history (Rust #61480
// `thread/realtime/list`). The stream is independent of turn pagination and a
// live runtime, so clients can catch up on sealed events without hydrating a
// conversation.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"codex_go/rollout"
)

type ThreadRealtimeOrder string

const (
	ThreadRealtimeOrderObservedAt ThreadRealtimeOrder = "observedAt"
	ThreadRealtimeOrderPosition   ThreadRealtimeOrder = "position"
)

type ThreadRealtimeCoverage string

const (
	ThreadRealtimeCoverageUnsupported ThreadRealtimeCoverage = "unsupported"
	ThreadRealtimeCoveragePending     ThreadRealtimeCoverage = "pending"
	ThreadRealtimeCoverageReady       ThreadRealtimeCoverage = "ready"
)

type ThreadRealtimeListParams struct {
	ThreadID                string
	SessionID               *string
	FromObservedAtMS        *int64
	ToObservedAtMS          *int64
	Order                   ThreadRealtimeOrder
	AfterPosition           *uint64
	EventID                 *string
	Cursor                  *string
	PageSize                int
	SnapshotEnd             *uint64
	RequiredThroughPosition *uint64
}

type ThreadRealtimeEntry struct {
	Position     uint64
	ObservedAtMS *int64
	ItemJSON     json.RawMessage
}

type ThreadRealtimePage struct {
	Data                      []ThreadRealtimeEntry
	NextCursor                *string
	SnapshotEnd               uint64
	ProjectedThroughItemIndex uint64
	Coverage                  ThreadRealtimeCoverage
}

type threadRealtimeCursorQuery struct {
	ThreadID                string              `json:"thread_id"`
	SessionID               *string             `json:"session_id"`
	FromObservedAtMS        *int64              `json:"from_observed_at_ms"`
	ToObservedAtMS          *int64              `json:"to_observed_at_ms"`
	Order                   ThreadRealtimeOrder `json:"order"`
	AfterPosition           *uint64             `json:"after_position"`
	EventID                 *string             `json:"event_id"`
	RequiredThroughPosition *uint64             `json:"required_through_position"`
}

type threadRealtimeCursor struct {
	Version      uint8                     `json:"version"`
	RolloutID    string                    `json:"rollout_id"`
	Query        threadRealtimeCursorQuery `json:"query"`
	SnapshotEnd  uint64                    `json:"snapshot_end"`
	ObservedAtMS *int64                    `json:"observed_at_ms"`
	Position     uint64                    `json:"position"`
}

// ListThreadRealtime reads an immutable page from a paginated thread's
// persisted realtime stream. Legacy and missing history report unsupported
// coverage rather than a method failure.
func (r *StateRuntime) ListThreadRealtime(ctx context.Context, params ThreadRealtimeListParams) (*ThreadRealtimePage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if threadID == "" {
		return nil, invalidThreadHistory("thread id is required")
	}
	if params.PageSize <= 0 || params.PageSize > 500 {
		return nil, invalidThreadHistory("realtime page size must be between 1 and 500")
	}
	if params.FromObservedAtMS != nil && params.ToObservedAtMS != nil && *params.FromObservedAtMS >= *params.ToObservedAtMS {
		return nil, invalidThreadHistory("realtime observation time range must be nonempty")
	}
	order, err := normalizeThreadRealtimeOrder(params.Order)
	if err != nil {
		return nil, err
	}
	params.ThreadID = threadID
	params.Order = order
	if _, err := threadRealtimeSQLitePosition("after position", params.AfterPosition); err != nil {
		return nil, err
	}
	if _, err := threadRealtimeSQLitePosition("snapshot end", params.SnapshotEnd); err != nil {
		return nil, err
	}
	if _, err := threadRealtimeSQLitePosition("required through position", params.RequiredThroughPosition); err != nil {
		return nil, err
	}
	mode, found, err := r.ThreadHistoryMode(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if !found || !strings.EqualFold(strings.TrimSpace(mode), "paginated") {
		return emptyThreadRealtimePage(ThreadRealtimeCoverageUnsupported, 0, 0), nil
	}
	path, meta, err := r.resolveLineageRolloutCopy(ctx, threadID, threadID)
	if err != nil {
		// Rust resolves a paginated thread whose rollout has already been pruned
		// to "no rollout" and answers from the (empty) projection instead of
		// failing the read.
		if !r.threadHasRealtimeRollout(ctx, threadID) {
			db, dbErr := r.ThreadHistoryDB(ctx)
			if dbErr != nil {
				return nil, dbErr
			}
			return readThreadRealtimePage(ctx, db, params, threadID, 0, 0)
		}
		return nil, err
	}
	firstPosition := uint64(0)
	if meta.SubagentHistoryStartOrdinal != nil {
		firstPosition = *meta.SubagentHistoryStartOrdinal
	} else if meta.HistoryBase != nil {
		firstPosition = meta.HistoryBase.EndOrdinalExclusive
	}
	// A copied fork without a boundary cannot distinguish its own frames from
	// the parent's copied positions.
	if strings.TrimSpace(meta.ForkedFromID) != "" && firstPosition == 0 {
		return emptyThreadRealtimePage(ThreadRealtimeCoverageUnsupported, 0, 0), nil
	}
	db, err := r.ThreadHistoryDB(ctx)
	if err != nil {
		return nil, err
	}
	initialOrdinal := uint64(0)
	if meta.HistoryBase != nil {
		initialOrdinal = meta.HistoryBase.EndOrdinalExclusive
	}
	if err := r.MaterializeThreadHistory(ctx, db, threadID, path, initialOrdinal, meta.SubagentHistoryStartOrdinal); err != nil {
		return nil, err
	}
	projection, err := LoadThreadHistoryProjectionState(ctx, db, threadID)
	if err != nil {
		return nil, err
	}
	projectedEnd := uint64(0)
	if projection != nil {
		projectedEnd = projection.NextRolloutOrdinal
	}
	return readThreadRealtimePage(ctx, db, params, threadID, firstPosition, projectedEnd)
}

// threadHasRealtimeRollout reports whether any rollout copy exists for the
// thread, either indexed by the state database or present in managed storage.
func (r *StateRuntime) threadHasRealtimeRollout(ctx context.Context, threadID string) bool {
	if indexed, found, err := r.FindRolloutPathByID(ctx, threadID, nil); err == nil && found {
		if _, ok := rollout.ExistingRolloutPath(indexed); ok {
			return true
		}
	}
	copies, err := rollout.FindThreadRolloutCopies(r.sqlite.Home(), threadID)
	return err == nil && len(copies) > 0
}

func normalizeThreadRealtimeOrder(order ThreadRealtimeOrder) (ThreadRealtimeOrder, error) {
	switch order {
	case "":
		return ThreadRealtimeOrderObservedAt, nil
	case ThreadRealtimeOrderObservedAt, ThreadRealtimeOrderPosition:
		return order, nil
	default:
		return "", invalidThreadHistory(fmt.Sprintf("unknown realtime order %q", order))
	}
}

func threadRealtimeSQLitePosition(name string, value *uint64) (int64, error) {
	if value == nil {
		return 0, nil
	}
	if *value > maxSQLiteInteger {
		return 0, invalidThreadHistory("realtime " + name + " exceeds the SQLite integer range")
	}
	return int64(*value), nil
}

func emptyThreadRealtimePage(coverage ThreadRealtimeCoverage, snapshotEnd, projectedThrough uint64) *ThreadRealtimePage {
	return &ThreadRealtimePage{
		Data:                      []ThreadRealtimeEntry{},
		SnapshotEnd:               snapshotEnd,
		ProjectedThroughItemIndex: projectedThrough,
		Coverage:                  coverage,
	}
}

func readThreadRealtimePage(ctx context.Context, db *sql.DB, params ThreadRealtimeListParams, rolloutID string, firstPosition uint64, projectedEnd uint64) (*ThreadRealtimePage, error) {
	if db == nil {
		return nil, fmt.Errorf("thread history database is nil")
	}
	if params.PageSize <= 0 || params.PageSize > 500 {
		return nil, invalidThreadHistory("realtime page size must be between 1 and 500")
	}
	if params.FromObservedAtMS != nil && params.ToObservedAtMS != nil && *params.FromObservedAtMS >= *params.ToObservedAtMS {
		return nil, invalidThreadHistory("realtime observation time range must be nonempty")
	}
	order, err := normalizeThreadRealtimeOrder(params.Order)
	if err != nil {
		return nil, err
	}
	params.Order = order
	query := threadRealtimeCursorQuery{
		ThreadID:                rolloutID,
		SessionID:               params.SessionID,
		FromObservedAtMS:        params.FromObservedAtMS,
		ToObservedAtMS:          params.ToObservedAtMS,
		Order:                   order,
		AfterPosition:           params.AfterPosition,
		EventID:                 params.EventID,
		RequiredThroughPosition: params.RequiredThroughPosition,
	}
	cursor, err := parseThreadRealtimeCursor(params.Cursor)
	if err != nil {
		return nil, err
	}
	if cursor != nil {
		explicitSnapshotMismatch := params.SnapshotEnd != nil && *params.SnapshotEnd != cursor.SnapshotEnd
		if cursor.Version != 1 || cursor.RolloutID != rolloutID ||
			!reflect.DeepEqual(cursor.Query, query) || explicitSnapshotMismatch ||
			cursor.Position < firstPosition || cursor.Position >= cursor.SnapshotEnd {
			return nil, invalidThreadHistory("realtime cursor does not match this query")
		}
	}
	snapshotEnd := projectedEnd
	if cursor != nil {
		snapshotEnd = cursor.SnapshotEnd
	} else if params.SnapshotEnd != nil {
		snapshotEnd = *params.SnapshotEnd
	}
	exactIDUsesCurrentSnapshot := params.EventID != nil && cursor == nil && params.SnapshotEnd == nil
	if projectedEnd < snapshotEnd ||
		(params.RequiredThroughPosition != nil && projectedEnd <= *params.RequiredThroughPosition) {
		return emptyThreadRealtimePage(ThreadRealtimeCoveragePending, snapshotEnd, projectedEnd), nil
	}
	snapshotEndSQL, err := threadRealtimeSQLitePosition("snapshot end", &snapshotEnd)
	if err != nil {
		return nil, err
	}
	firstPositionSQL, err := threadRealtimeSQLitePosition("first realtime position", &firstPosition)
	if err != nil {
		return nil, err
	}
	const observedAt = "json_extract(item_json, '$.observed_at_ms')"
	sqlText := `SELECT rollout_ordinal, ` + observedAt + `, item_json
FROM thread_realtime_items
WHERE thread_id = ? AND rollout_ordinal >= ?
  AND ` + observedAt + ` IS NOT NULL AND item_type != 'transcript_segment'`
	args := []any{rolloutID, firstPositionSQL}
	if !exactIDUsesCurrentSnapshot {
		sqlText += ` AND rollout_ordinal < ?`
		args = append(args, snapshotEndSQL)
	}
	if params.AfterPosition != nil {
		after, err := threadRealtimeSQLitePosition("after position", params.AfterPosition)
		if err != nil {
			return nil, err
		}
		sqlText += ` AND rollout_ordinal > ?`
		args = append(args, after)
	}
	if params.SessionID != nil {
		sqlText += ` AND json_extract(item_json, '$.realtime_session_id') = ?`
		args = append(args, *params.SessionID)
	}
	if params.EventID != nil {
		sqlText += ` AND item_id = ?`
		args = append(args, *params.EventID)
	}
	if params.FromObservedAtMS != nil {
		sqlText += ` AND ` + observedAt + ` >= ?`
		args = append(args, *params.FromObservedAtMS)
	}
	if params.ToObservedAtMS != nil {
		sqlText += ` AND ` + observedAt + ` < ?`
		args = append(args, *params.ToObservedAtMS)
	}
	if cursor != nil {
		position, err := threadRealtimeSQLitePosition("cursor position", &cursor.Position)
		if err != nil {
			return nil, err
		}
		if order == ThreadRealtimeOrderPosition {
			sqlText += ` AND rollout_ordinal > ?`
			args = append(args, position)
		} else {
			if cursor.ObservedAtMS == nil {
				return nil, invalidThreadHistory("realtime cursor is missing its observation time")
			}
			sqlText += ` AND (` + observedAt + `, rollout_ordinal) > (?, ?)`
			args = append(args, *cursor.ObservedAtMS, position)
		}
	}
	if order == ThreadRealtimeOrderObservedAt {
		sqlText += ` ORDER BY ` + observedAt + `, rollout_ordinal`
	} else {
		sqlText += ` ORDER BY rollout_ordinal`
	}
	sqlText += ` LIMIT ?`
	args = append(args, params.PageSize+1)

	transaction, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin realtime history read: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	rows, err := transaction.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("list realtime history: %w", err)
	}
	type row struct {
		position uint64
		observed *int64
		itemJSON json.RawMessage
	}
	readRows := make([]row, 0, params.PageSize+1)
	for rows.Next() {
		var position int64
		var observed sql.NullInt64
		var itemJSON string
		if err := rows.Scan(&position, &observed, &itemJSON); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan realtime history: %w", err)
		}
		if position < 0 {
			_ = rows.Close()
			return nil, fmt.Errorf("realtime history contains a negative position")
		}
		var observedAtMS *int64
		if observed.Valid {
			value := observed.Int64
			observedAtMS = &value
		}
		readRows = append(readRows, row{position: uint64(position), observed: observedAtMS, itemJSON: json.RawMessage(itemJSON)})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("list realtime history: %w", err)
	}
	_ = rows.Close()
	if exactIDUsesCurrentSnapshot {
		for _, entry := range readRows {
			if entry.position >= snapshotEnd {
				return emptyThreadRealtimePage(ThreadRealtimeCoveragePending, snapshotEnd, projectedEnd), nil
			}
		}
	}
	hasMore := len(readRows) > params.PageSize
	if hasMore {
		readRows = readRows[:params.PageSize]
	}
	data := make([]ThreadRealtimeEntry, 0, len(readRows))
	for _, entry := range readRows {
		data = append(data, ThreadRealtimeEntry{Position: entry.position, ObservedAtMS: entry.observed, ItemJSON: entry.itemJSON})
	}
	var nextCursor *string
	if hasMore && len(data) > 0 {
		last := data[len(data)-1]
		encoded, err := json.Marshal(threadRealtimeCursor{
			Version:      1,
			RolloutID:    rolloutID,
			Query:        query,
			SnapshotEnd:  snapshotEnd,
			ObservedAtMS: last.ObservedAtMS,
			Position:     last.Position,
		})
		if err != nil {
			return nil, fmt.Errorf("encode realtime cursor: %w", err)
		}
		value := string(encoded)
		nextCursor = &value
	}
	coverage := ThreadRealtimeCoverageReady
	if params.EventID != nil && len(data) == 0 {
		var exists bool
		if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1 FROM thread_realtime_items
    WHERE thread_id = ? AND item_id = ? AND rollout_ordinal >= ?
)`, rolloutID, *params.EventID, firstPositionSQL).Scan(&exists); err != nil {
			return nil, fmt.Errorf("check realtime event coverage: %w", err)
		}
		if !exists {
			coverage = ThreadRealtimeCoveragePending
		}
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("commit realtime history read: %w", err)
	}
	return &ThreadRealtimePage{
		Data:                      data,
		NextCursor:                nextCursor,
		SnapshotEnd:               snapshotEnd,
		ProjectedThroughItemIndex: projectedEnd,
		Coverage:                  coverage,
	}, nil
}

// ThreadRealtimeSession is one logical realtime call recovered from durable
// history (Rust ThreadStore::load_realtime_session).
type ThreadRealtimeSession struct {
	SessionID string
	Items     []ThreadRealtimeEntry
}

// LoadThreadRealtimeSession loads one logical call from authoritative persisted
// history. A session without a persisted start record reports no session, so a
// lagging projection fails closed instead of inventing one.
func (r *StateRuntime) LoadThreadRealtimeSession(ctx context.Context, threadID string, sessionID string) (*ThreadRealtimeSession, error) {
	if r == nil {
		return nil, invalidThreadHistory("thread history is not available")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	threadID = strings.TrimSpace(threadID)
	sessionID = strings.TrimSpace(sessionID)
	if threadID == "" || sessionID == "" {
		return nil, invalidThreadHistory("thread id and realtime session id are required")
	}
	db, err := r.ThreadHistoryDB(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
SELECT rollout_ordinal, json_extract(item_json, '$.observed_at_ms'), item_json, item_type
FROM thread_realtime_items
WHERE thread_id = ? AND json_extract(item_json, '$.realtime_session_id') = ?
ORDER BY rollout_ordinal, item_id`, threadID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load realtime session: %w", err)
	}
	defer func() { _ = rows.Close() }()
	session := &ThreadRealtimeSession{SessionID: sessionID}
	started := false
	for rows.Next() {
		var entry ThreadRealtimeEntry
		var observed sql.NullInt64
		var itemJSON string
		var itemType string
		if err := rows.Scan(&entry.Position, &observed, &itemJSON, &itemType); err != nil {
			return nil, fmt.Errorf("scan realtime session item: %w", err)
		}
		if observed.Valid {
			value := observed.Int64
			entry.ObservedAtMS = &value
		}
		entry.ItemJSON = json.RawMessage(itemJSON)
		if itemType == "realtime_session_started" {
			started = true
		}
		session.Items = append(session.Items, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read realtime session items: %w", err)
	}
	if !started {
		return nil, nil
	}
	return session, nil
}

// LatestThreadRealtimeSession returns the newest logical session id from
// persisted start records, including a closed session (Rust
// ThreadStore::load_realtime_session_head). A newly requested id that has no
// persisted session yet falls back to this head.
func (r *StateRuntime) LatestThreadRealtimeSession(ctx context.Context, threadID string) (string, bool, error) {
	if r == nil {
		return "", false, invalidThreadHistory("thread history is not available")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", false, invalidThreadHistory("thread id is required")
	}
	db, err := r.ThreadHistoryDB(ctx)
	if err != nil {
		return "", false, err
	}
	var sessionID sql.NullString
	err = db.QueryRowContext(ctx, `
SELECT json_extract(item_json, '$.realtime_session_id')
FROM thread_realtime_items
WHERE thread_id = ? AND item_type = 'realtime_session_started'
ORDER BY rollout_ordinal DESC, item_id DESC
LIMIT 1`, threadID).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read latest realtime session: %w", err)
	}
	value := strings.TrimSpace(sessionID.String)
	if value == "" {
		return "", false, nil
	}
	return value, true, nil
}

// ThreadRealtimeEnrolled reports whether durable history already holds a
// canonical realtime session start. Rust #62271 keeps such a thread enrolled
// after the `features.realtime_frames_local` flag is turned off, so a call that
// is still streaming does not lose its frames.
func (r *StateRuntime) ThreadRealtimeEnrolled(ctx context.Context, threadID string) (bool, error) {
	if r == nil {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false, nil
	}
	db, err := r.ThreadHistoryDB(ctx)
	if err != nil {
		return false, err
	}
	var exists bool
	if err := db.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1 FROM thread_realtime_items
    WHERE thread_id = ? AND item_type = 'realtime_session_started'
)`, threadID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check realtime enrollment: %w", err)
	}
	return exists, nil
}

func parseThreadRealtimeCursor(raw *string) (*threadRealtimeCursor, error) {
	if raw == nil {
		return nil, nil
	}
	var cursor threadRealtimeCursor
	if err := json.Unmarshal([]byte(*raw), &cursor); err != nil {
		return nil, invalidThreadHistory("invalid realtime cursor")
	}
	return &cursor, nil
}
