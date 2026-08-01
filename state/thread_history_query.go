package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codex_go/rollout"
)

type ThreadHistorySortDirection string

const (
	ThreadHistorySortAsc  ThreadHistorySortDirection = "asc"
	ThreadHistorySortDesc ThreadHistorySortDirection = "desc"
)

type ThreadHistoryItemsView string

const (
	ThreadHistoryItemsNotLoaded ThreadHistoryItemsView = "notLoaded"
	ThreadHistoryItemsSummary   ThreadHistoryItemsView = "summary"
)

type ThreadHistoryErrorKind string

const (
	ThreadHistoryInvalidRequest ThreadHistoryErrorKind = "invalidRequest"
	ThreadHistoryUnsupported    ThreadHistoryErrorKind = "unsupported"
	ThreadHistoryNotFound       ThreadHistoryErrorKind = "notFound"
)

type ThreadHistoryError struct {
	Kind      ThreadHistoryErrorKind
	Operation string
	ThreadID  string
	Message   string
}

func (e *ThreadHistoryError) Error() string {
	if e == nil {
		return "thread history error"
	}
	if e.Message != "" {
		return e.Message
	}
	switch e.Kind {
	case ThreadHistoryUnsupported:
		return e.Operation + " is not supported yet"
	case ThreadHistoryNotFound:
		return "no rollout found for thread id " + e.ThreadID
	default:
		return "invalid thread history request"
	}
}

type ThreadHistoryItem struct {
	TurnID             string
	ItemID             string
	UpdatedAtOrdinal   uint64
	CreatedAtMS        int64
	ItemJSON           json.RawMessage
	physicalThreadID   string
	physicalRolloutPos int64
}

type ThreadHistoryTurn struct {
	TurnID       string
	Items        []ThreadHistoryItem
	ItemsView    ThreadHistoryItemsView
	Status       string
	ErrorJSON    json.RawMessage
	StartedAt    *int64
	CompletedAt  *int64
	DurationMS   *int64
	physicalID   string
	rolloutPos   int64
	firstUserID  *string
	finalAgentID *string
}

type ThreadHistoryItemsPage struct {
	Items           []ThreadHistoryItem
	NextCursor      *string
	BackwardsCursor *string
}

type ThreadHistoryTurnsPage struct {
	Turns           []ThreadHistoryTurn
	NextCursor      *string
	BackwardsCursor *string
}

type ThreadHistoryListItemsParams struct {
	ThreadID      string
	TurnID        *string
	Cursor        *string
	PageSize      int
	SortDirection ThreadHistorySortDirection
}

type ThreadHistoryListTurnsParams struct {
	ThreadID      string
	Cursor        *string
	PageSize      int
	SortDirection ThreadHistorySortDirection
	ItemsView     ThreadHistoryItemsView
}

type historyCursorScope struct {
	Kind string `json:"kind"`
}

type historyCursor struct {
	RequestedThreadID string             `json:"requestedThreadId"`
	RolloutOrdinal    uint64             `json:"rolloutOrdinal"`
	IncludeAnchor     bool               `json:"includeAnchor"`
	Scope             historyCursorScope `json:"scope"`
}

const (
	historyCursorTurns = "turns"
	historyCursorItems = "itemsByCreatedAtOrdinal"
)

type threadHistoryLineageSegment struct {
	ThreadID string
	Path     string
	Start    uint64
	End      *rollout.HistoryPosition
	Meta     *rollout.SessionMeta
}

func (r *StateRuntime) ThreadHistoryMode(ctx context.Context, threadID string) (string, bool, error) {
	if r == nil || r.stateDB == nil {
		return "", false, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var mode string
	err := r.stateDB.QueryRowContext(ctx, `SELECT history_mode FROM threads WHERE id = ?`, strings.TrimSpace(threadID)).Scan(&mode)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read thread history mode: %w", err)
	}
	return mode, true, nil
}

func (r *StateRuntime) ListThreadHistoryItems(ctx context.Context, params ThreadHistoryListItemsParams) (*ThreadHistoryItemsPage, error) {
	lineage, db, err := r.prepareThreadHistoryRead(ctx, params.ThreadID, "list_items")
	if err != nil {
		return nil, err
	}
	if params.PageSize <= 0 {
		return nil, invalidThreadHistory("page size must be positive")
	}
	direction := params.SortDirection
	if direction == "" {
		direction = ThreadHistorySortAsc
	}
	cursor, err := parseHistoryCursor(params.Cursor, params.ThreadID, historyCursorItems)
	if err != nil {
		return nil, err
	}
	segments, err := historySegmentsFromCursor(lineage, direction, cursor)
	if err != nil {
		return nil, err
	}
	rows := make([]ThreadHistoryItem, 0, params.PageSize+1)
	for _, segment := range segments {
		remaining := params.PageSize + 1 - len(rows)
		if remaining <= 0 {
			break
		}
		query := `
SELECT turn_id, item_id, rollout_ordinal, updated_at_ordinal, created_at_ms, item_json
FROM thread_items
WHERE thread_id = ? AND rollout_ordinal >= ?`
		args := []any{segment.segment.ThreadID, sqliteInt(segment.segment.Start)}
		if segment.segment.End != nil {
			query += ` AND rollout_ordinal < ?`
			args = append(args, sqliteInt(segment.segment.End.EndOrdinalExclusive))
		}
		if params.TurnID != nil {
			query += ` AND turn_id = ?`
			args = append(args, *params.TurnID)
		}
		query, args = appendHistoryCursorClause(query, args, direction, segment.cursor)
		query += ` ORDER BY rollout_ordinal ` + historySQLDirection(direction) + ` LIMIT ?`
		args = append(args, remaining)
		result, queryErr := db.QueryContext(ctx, query, args...)
		if queryErr != nil {
			return nil, fmt.Errorf("list thread history items: %w", queryErr)
		}
		for result.Next() {
			var item ThreadHistoryItem
			var rolloutOrdinal, updatedOrdinal int64
			var itemJSON string
			if scanErr := result.Scan(&item.TurnID, &item.ItemID, &rolloutOrdinal, &updatedOrdinal, &item.CreatedAtMS, &itemJSON); scanErr != nil {
				_ = result.Close()
				return nil, fmt.Errorf("scan thread history item: %w", scanErr)
			}
			if rolloutOrdinal < 0 || updatedOrdinal < 0 {
				_ = result.Close()
				return nil, errors.New("invalid stored item rollout ordinal")
			}
			item.UpdatedAtOrdinal = uint64(updatedOrdinal)
			item.ItemJSON = json.RawMessage(itemJSON)
			item.physicalThreadID = segment.segment.ThreadID
			item.physicalRolloutPos = rolloutOrdinal
			rows = append(rows, item)
		}
		if queryErr = result.Err(); queryErr != nil {
			_ = result.Close()
			return nil, fmt.Errorf("list thread history items: %w", queryErr)
		}
		_ = result.Close()
	}
	next, backwards, rows, err := finishHistoryItemPage(params.ThreadID, rows, params.PageSize)
	if err != nil {
		return nil, err
	}
	return &ThreadHistoryItemsPage{Items: rows, NextCursor: next, BackwardsCursor: backwards}, nil
}

func (r *StateRuntime) ListThreadHistoryTurns(ctx context.Context, params ThreadHistoryListTurnsParams) (*ThreadHistoryTurnsPage, error) {
	lineage, db, err := r.prepareThreadHistoryRead(ctx, params.ThreadID, "list_turns")
	if err != nil {
		return nil, err
	}
	if params.PageSize <= 0 {
		return nil, invalidThreadHistory("page size must be positive")
	}
	direction := params.SortDirection
	if direction == "" {
		direction = ThreadHistorySortDesc
	}
	cursor, err := parseHistoryCursor(params.Cursor, params.ThreadID, historyCursorTurns)
	if err != nil {
		return nil, err
	}
	segments, err := historySegmentsFromCursor(lineage, direction, cursor)
	if err != nil {
		return nil, err
	}
	rows := make([]ThreadHistoryTurn, 0, params.PageSize+1)
	for _, selected := range segments {
		remaining := params.PageSize + 1 - len(rows)
		if remaining <= 0 {
			break
		}
		query := `
SELECT turn_id, rollout_ordinal, status, error_json, started_at, completed_at,
       duration_ms, first_user_item_id, final_agent_item_id
FROM thread_turns
WHERE thread_id = ? AND rollout_ordinal >= ?`
		args := []any{selected.segment.ThreadID, sqliteInt(selected.segment.Start)}
		if selected.segment.End != nil {
			query += ` AND rollout_ordinal < ?`
			args = append(args, sqliteInt(selected.segment.End.EndOrdinalExclusive))
		}
		for _, newer := range lineage[selected.index+1:] {
			query += ` AND NOT EXISTS (SELECT 1 FROM thread_turns AS newer_turn WHERE newer_turn.thread_id = ? AND newer_turn.turn_id = thread_turns.turn_id AND newer_turn.rollout_ordinal >= ?`
			args = append(args, newer.ThreadID, sqliteInt(newer.Start))
			if newer.End != nil {
				query += ` AND newer_turn.rollout_ordinal < ?`
				args = append(args, sqliteInt(newer.End.EndOrdinalExclusive))
			}
			query += `)`
		}
		query, args = appendHistoryCursorClause(query, args, direction, selected.cursor)
		query += ` ORDER BY rollout_ordinal ` + historySQLDirection(direction) + ` LIMIT ?`
		args = append(args, remaining)
		result, queryErr := db.QueryContext(ctx, query, args...)
		if queryErr != nil {
			return nil, fmt.Errorf("list thread history turns: %w", queryErr)
		}
		for result.Next() {
			var turn ThreadHistoryTurn
			var ordinal int64
			var errorJSON sql.NullString
			var startedAt, completedAt, durationMS sql.NullInt64
			var firstUserID, finalAgentID sql.NullString
			if scanErr := result.Scan(&turn.TurnID, &ordinal, &turn.Status, &errorJSON, &startedAt, &completedAt, &durationMS, &firstUserID, &finalAgentID); scanErr != nil {
				_ = result.Close()
				return nil, fmt.Errorf("scan thread history turn: %w", scanErr)
			}
			if ordinal < 0 {
				_ = result.Close()
				return nil, errors.New("invalid stored turn rollout ordinal")
			}
			switch turn.Status {
			case "completed", "interrupted", "failed", "inProgress":
			default:
				_ = result.Close()
				return nil, fmt.Errorf("unknown stored turn status: %s", turn.Status)
			}
			turn.ErrorJSON = nullStringJSON(errorJSON)
			turn.StartedAt = nullInt64Ptr(startedAt)
			turn.CompletedAt = nullInt64Ptr(completedAt)
			turn.DurationMS = nullInt64Ptr(durationMS)
			turn.firstUserID = nullStringPtr(firstUserID)
			turn.finalAgentID = nullStringPtr(finalAgentID)
			turn.physicalID = selected.segment.ThreadID
			turn.rolloutPos = ordinal
			turn.ItemsView = params.ItemsView
			rows = append(rows, turn)
		}
		if queryErr = result.Err(); queryErr != nil {
			_ = result.Close()
			return nil, fmt.Errorf("list thread history turns: %w", queryErr)
		}
		_ = result.Close()
	}
	next, backwards, rows, err := finishHistoryTurnPage(params.ThreadID, rows, params.PageSize)
	if err != nil {
		return nil, err
	}
	if params.ItemsView == ThreadHistoryItemsSummary {
		for i := range rows {
			items, loadErr := loadThreadHistorySummaryItems(ctx, db, lineage, &rows[i])
			if loadErr != nil {
				return nil, loadErr
			}
			rows[i].Items = items
		}
	}
	return &ThreadHistoryTurnsPage{Turns: rows, NextCursor: next, BackwardsCursor: backwards}, nil
}

func (r *StateRuntime) prepareThreadHistoryRead(ctx context.Context, threadID, operation string) ([]threadHistoryLineageSegment, *sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	threadID = strings.TrimSpace(threadID)
	mode, found, err := r.ThreadHistoryMode(ctx, threadID)
	if err != nil {
		return nil, nil, err
	}
	if !found || !strings.EqualFold(strings.TrimSpace(mode), "paginated") {
		return nil, nil, &ThreadHistoryError{Kind: ThreadHistoryUnsupported, Operation: operation}
	}
	lineage, err := r.resolveThreadHistoryLineage(ctx, threadID)
	if err != nil {
		return nil, nil, err
	}
	db, err := r.ThreadHistoryDB(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, segment := range lineage {
		initialOrdinal := uint64(0)
		if segment.Meta.HistoryBase != nil {
			initialOrdinal = segment.Meta.HistoryBase.EndOrdinalExclusive
		}
		if err := MaterializeThreadHistory(ctx, db, segment.ThreadID, segment.Path, initialOrdinal, segment.Meta.SubagentHistoryStartOrdinal); err != nil {
			return nil, nil, err
		}
	}
	return lineage, db, nil
}

func (r *StateRuntime) resolveThreadHistoryLineage(ctx context.Context, requestedThreadID string) ([]threadHistoryLineageSegment, error) {
	segments := make([]threadHistoryLineageSegment, 0, 1)
	seen := map[string]bool{}
	threadID := requestedThreadID
	var end *rollout.HistoryPosition
	for {
		if seen[threadID] {
			return nil, invalidPaginatedLineage(requestedThreadID, "cycle detected")
		}
		seen[threadID] = true
		path, found, err := r.FindRolloutPathByID(ctx, threadID, nil)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, invalidPaginatedLineage(threadID, "missing source rollout")
		}
		existing, ok := rollout.ExistingRolloutPath(path)
		if !ok {
			return nil, invalidPaginatedLineage(threadID, "missing source rollout")
		}
		meta, err := rollout.FirstSessionMeta(existing)
		if err != nil {
			return nil, fmt.Errorf("read lineage metadata %s: %w", existing, err)
		}
		if meta.ID != threadID {
			return nil, invalidPaginatedLineage(requestedThreadID, "source rollout belongs to another thread")
		}
		if !strings.EqualFold(strings.TrimSpace(meta.HistoryMode), "paginated") {
			return nil, invalidPaginatedLineage(requestedThreadID, "source rollout is not paginated")
		}
		if end != nil {
			if end.EndOrdinalExclusive == 0 {
				return nil, invalidPaginatedLineage(requestedThreadID, "cutoff cannot include source session metadata")
			}
			if byteLength, lengthErr := rollout.RolloutByteLength(existing); lengthErr != nil {
				return nil, lengthErr
			} else if end.EndByteOffset > byteLength {
				return nil, invalidPaginatedLineage(requestedThreadID, "cutoff byte offset is past the source rollout")
			}
		}
		start := uint64(1)
		if meta.HistoryBase != nil {
			if meta.HistoryBase.EndOrdinalExclusive == ^uint64(0) {
				return nil, invalidPaginatedLineage(requestedThreadID, "source ordinal overflow")
			}
			start = meta.HistoryBase.EndOrdinalExclusive + 1
		}
		segments = append(segments, threadHistoryLineageSegment{ThreadID: threadID, Path: existing, Start: start, End: cloneRolloutHistoryPosition(end), Meta: meta})
		if meta.HistoryBase == nil {
			break
		}
		threadID = meta.HistoryBase.ThreadID
		end = cloneRolloutHistoryPosition(meta.HistoryBase)
	}
	for left, right := 0, len(segments)-1; left < right; left, right = left+1, right-1 {
		segments[left], segments[right] = segments[right], segments[left]
	}
	return segments, nil
}

type selectedHistorySegment struct {
	index   int
	segment threadHistoryLineageSegment
	cursor  *historyCursor
}

func historySegmentsFromCursor(lineage []threadHistoryLineageSegment, direction ThreadHistorySortDirection, cursor *historyCursor) ([]selectedHistorySegment, error) {
	cursorIndex := -1
	if cursor != nil {
		for i, segment := range lineage {
			if cursor.RolloutOrdinal >= segment.Start && (segment.End == nil || cursor.RolloutOrdinal < segment.End.EndOrdinalExclusive) {
				cursorIndex = i
				break
			}
		}
		if cursorIndex < 0 {
			return nil, invalidThreadHistory("invalid cursor: position outside thread lineage")
		}
	}
	selected := make([]selectedHistorySegment, 0, len(lineage))
	if direction == ThreadHistorySortDesc {
		start := len(lineage) - 1
		if cursorIndex >= 0 {
			start = cursorIndex
		}
		for i := start; i >= 0; i-- {
			var segmentCursor *historyCursor
			if i == cursorIndex {
				segmentCursor = cursor
			}
			selected = append(selected, selectedHistorySegment{index: i, segment: lineage[i], cursor: segmentCursor})
		}
		return selected, nil
	}
	start := 0
	if cursorIndex >= 0 {
		start = cursorIndex
	}
	for i := start; i < len(lineage); i++ {
		var segmentCursor *historyCursor
		if i == cursorIndex {
			segmentCursor = cursor
		}
		selected = append(selected, selectedHistorySegment{index: i, segment: lineage[i], cursor: segmentCursor})
	}
	return selected, nil
}

func parseHistoryCursor(value *string, threadID, scope string) (*historyCursor, error) {
	if value == nil {
		return nil, nil
	}
	var cursor historyCursor
	if err := json.Unmarshal([]byte(*value), &cursor); err != nil || cursor.RequestedThreadID != threadID || cursor.Scope.Kind != scope {
		return nil, invalidThreadHistory("invalid cursor: " + *value)
	}
	return &cursor, nil
}

func serializeHistoryCursor(threadID, scope string, ordinal int64, includeAnchor bool) (*string, error) {
	if ordinal < 0 {
		return nil, invalidThreadHistory("invalid cursor: negative rollout ordinal")
	}
	data, err := json.Marshal(historyCursor{RequestedThreadID: threadID, RolloutOrdinal: uint64(ordinal), IncludeAnchor: includeAnchor, Scope: historyCursorScope{Kind: scope}})
	if err != nil {
		return nil, err
	}
	value := string(data)
	return &value, nil
}

func appendHistoryCursorClause(query string, args []any, direction ThreadHistorySortDirection, cursor *historyCursor) (string, []any) {
	if cursor == nil {
		return query, args
	}
	comparator := ">"
	if direction == ThreadHistorySortDesc {
		comparator = "<"
	}
	if cursor.IncludeAnchor {
		comparator += "="
	}
	return query + ` AND rollout_ordinal ` + comparator + ` ?`, append(args, sqliteInt(cursor.RolloutOrdinal))
}

func historySQLDirection(direction ThreadHistorySortDirection) string {
	if direction == ThreadHistorySortDesc {
		return "DESC"
	}
	return "ASC"
}

func finishHistoryItemPage(threadID string, rows []ThreadHistoryItem, pageSize int) (*string, *string, []ThreadHistoryItem, error) {
	hasMore := len(rows) > pageSize
	if hasMore {
		rows = rows[:pageSize]
	}
	var backwards, next *string
	var err error
	if len(rows) > 0 {
		backwards, err = serializeHistoryCursor(threadID, historyCursorItems, rows[0].physicalRolloutPos, true)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if hasMore && len(rows) > 0 {
		next, err = serializeHistoryCursor(threadID, historyCursorItems, rows[len(rows)-1].physicalRolloutPos, false)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return next, backwards, rows, nil
}

func finishHistoryTurnPage(threadID string, rows []ThreadHistoryTurn, pageSize int) (*string, *string, []ThreadHistoryTurn, error) {
	hasMore := len(rows) > pageSize
	if hasMore {
		rows = rows[:pageSize]
	}
	var backwards, next *string
	var err error
	if len(rows) > 0 {
		backwards, err = serializeHistoryCursor(threadID, historyCursorTurns, rows[0].rolloutPos, true)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if hasMore && len(rows) > 0 {
		next, err = serializeHistoryCursor(threadID, historyCursorTurns, rows[len(rows)-1].rolloutPos, false)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return next, backwards, rows, nil
}

func loadThreadHistorySummaryItems(ctx context.Context, db *sql.DB, lineage []threadHistoryLineageSegment, turn *ThreadHistoryTurn) ([]ThreadHistoryItem, error) {
	owner, firstUserID, finalAgentID := turn.physicalID, turn.firstUserID, turn.finalAgentID
	if turn.Status == "interrupted" && firstUserID == nil && finalAgentID == nil {
		for _, segment := range lineage {
			row := db.QueryRowContext(ctx, `
SELECT first_user_item_id, final_agent_item_id
FROM thread_turns
WHERE thread_id = ? AND turn_id = ? AND rollout_ordinal >= ? AND (? IS NULL OR rollout_ordinal < ?)`,
				segment.ThreadID, turn.TurnID, sqliteInt(segment.Start), nullableHistoryEnd(segment.End), nullableHistoryEnd(segment.End))
			var first, final sql.NullString
			if err := row.Scan(&first, &final); err == sql.ErrNoRows {
				continue
			} else if err != nil {
				return nil, fmt.Errorf("find source turn: %w", err)
			}
			owner, firstUserID, finalAgentID = segment.ThreadID, nullStringPtr(first), nullStringPtr(final)
			break
		}
	}
	var segment *threadHistoryLineageSegment
	for i := range lineage {
		if lineage[i].ThreadID == owner {
			segment = &lineage[i]
			break
		}
	}
	if segment == nil {
		return []ThreadHistoryItem{}, nil
	}
	rows, err := db.QueryContext(ctx, `
SELECT turn_id, item_id, rollout_ordinal, updated_at_ordinal, created_at_ms, item_json
FROM thread_items
WHERE thread_id = ? AND turn_id = ? AND rollout_ordinal >= ?
  AND (? IS NULL OR rollout_ordinal < ?) AND (item_id = ? OR item_id = ?)
ORDER BY rollout_ordinal ASC`, owner, turn.TurnID, sqliteInt(segment.Start), nullableHistoryEnd(segment.End), nullableHistoryEnd(segment.End), firstUserID, finalAgentID)
	if err != nil {
		return nil, fmt.Errorf("load summary items: %w", err)
	}
	defer rows.Close()
	items := []ThreadHistoryItem{}
	for rows.Next() {
		var item ThreadHistoryItem
		var rolloutOrdinal, updatedOrdinal int64
		var itemJSON string
		if err := rows.Scan(&item.TurnID, &item.ItemID, &rolloutOrdinal, &updatedOrdinal, &item.CreatedAtMS, &itemJSON); err != nil {
			return nil, err
		}
		if rolloutOrdinal < 0 || updatedOrdinal < 0 {
			return nil, errors.New("invalid stored item ordinal")
		}
		item.UpdatedAtOrdinal = uint64(updatedOrdinal)
		item.ItemJSON = json.RawMessage(itemJSON)
		item.physicalThreadID = owner
		item.physicalRolloutPos = rolloutOrdinal
		items = append(items, item)
	}
	return items, rows.Err()
}

func nullableHistoryEnd(end *rollout.HistoryPosition) any {
	if end == nil {
		return nil
	}
	return sqliteInt(end.EndOrdinalExclusive)
}

func invalidPaginatedLineage(threadID, detail string) error {
	return invalidThreadHistory(fmt.Sprintf("invalid paginated history lineage for %s: %s", threadID, detail))
}

func invalidThreadHistory(message string) error {
	return &ThreadHistoryError{Kind: ThreadHistoryInvalidRequest, Message: message}
}

func cloneRolloutHistoryPosition(value *rollout.HistoryPosition) *rollout.HistoryPosition {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func nullStringJSON(value sql.NullString) json.RawMessage {
	if !value.Valid {
		return nil
	}
	return json.RawMessage(value.String)
}
