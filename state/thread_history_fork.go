package state

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"codex_go/rollout"
)

const (
	ThreadHistoryForkLatest      = "latest"
	ThreadHistoryForkThroughTurn = "throughTurn"
	ThreadHistoryForkBeforeTurn  = "beforeTurn"
)

type threadHistoryForkTurnRow struct {
	PhysicalThreadID     string
	RolloutOrdinal       int64
	RolloutByteOffset    sql.NullInt64
	RolloutEndOrdinal    sql.NullInt64
	RolloutEndByteOffset sql.NullInt64
	Status               string
}

// PreparePaginatedFork freezes a physical rollout prefix using the same
// lineage and SQLite positions as Rust's thread-store paginated fork path.
func (r *StateRuntime) PreparePaginatedFork(ctx context.Context, threadID, boundaryKind, turnID string) (*rollout.HistoryPosition, error) {
	if err := r.materializePaginatedLineageForReference(ctx, threadID); err != nil {
		return nil, err
	}
	lineage, db, err := r.prepareThreadHistoryRead(ctx, threadID, "prepare_fork")
	if err != nil {
		return nil, err
	}
	if len(lineage) == 0 {
		return nil, invalidThreadHistory("fork lineage has no source segment")
	}
	boundaryKind = strings.TrimSpace(boundaryKind)
	if boundaryKind == "" {
		boundaryKind = ThreadHistoryForkLatest
	}
	turnID = strings.TrimSpace(turnID)

	var position *rollout.HistoryPosition
	switch boundaryKind {
	case ThreadHistoryForkLatest:
		if turnID != "" {
			return nil, invalidThreadHistory("latest fork boundary must not include a turn id")
		}
		projection, err := LoadThreadHistoryProjectionState(ctx, db, strings.TrimSpace(threadID))
		if err != nil {
			return nil, err
		}
		if projection == nil {
			return nil, fmt.Errorf("missing projection state for paginated thread %s", threadID)
		}
		position = &rollout.HistoryPosition{
			ThreadID: strings.TrimSpace(threadID), EndOrdinalExclusive: projection.NextRolloutOrdinal, EndByteOffset: projection.NextRolloutByteOffset,
		}
	case ThreadHistoryForkThroughTurn:
		if turnID == "" {
			return nil, invalidThreadHistory("throughTurn fork boundary requires a turn id")
		}
		row, err := findThreadHistoryForkTurn(ctx, db, lineage, turnID, true)
		if err != nil {
			return nil, err
		}
		if row.Status == "inProgress" {
			return nil, invalidThreadHistory(fmt.Sprintf("lastTurnId '%s' identifies an in-progress turn", turnID))
		}
		if !row.RolloutEndOrdinal.Valid || !row.RolloutEndByteOffset.Valid {
			return nil, invalidThreadHistory(fmt.Sprintf("turn %s does not have persisted rollout positions", turnID))
		}
		endOrdinal, ok := nonNegativeUint64(row.RolloutEndOrdinal.Int64)
		if !ok || endOrdinal == ^uint64(0) {
			return nil, fmt.Errorf("invalid rollout position for turn %s", turnID)
		}
		endOffset, ok := nonNegativeUint64(row.RolloutEndByteOffset.Int64)
		if !ok {
			return nil, fmt.Errorf("invalid rollout position for turn %s", turnID)
		}
		position = &rollout.HistoryPosition{ThreadID: row.PhysicalThreadID, EndOrdinalExclusive: endOrdinal + 1, EndByteOffset: endOffset}
	case ThreadHistoryForkBeforeTurn:
		if turnID == "" {
			return nil, invalidThreadHistory("beforeTurn fork boundary requires a turn id")
		}
		row, err := findThreadHistoryForkTurn(ctx, db, lineage, turnID, false)
		if err != nil {
			return nil, err
		}
		if row.RolloutEndOrdinal.Valid && row.RolloutEndOrdinal.Int64 == row.RolloutOrdinal {
			return nil, invalidThreadHistory(fmt.Sprintf("turn %s does not have a persisted start boundary", turnID))
		}
		if !row.RolloutByteOffset.Valid {
			return nil, invalidThreadHistory(fmt.Sprintf("turn %s does not have persisted rollout positions", turnID))
		}
		ordinal, ok := nonNegativeUint64(row.RolloutOrdinal)
		if !ok {
			return nil, fmt.Errorf("invalid rollout position for turn %s", turnID)
		}
		offset, ok := nonNegativeUint64(row.RolloutByteOffset.Int64)
		if !ok {
			return nil, fmt.Errorf("invalid rollout position for turn %s", turnID)
		}
		position = &rollout.HistoryPosition{ThreadID: row.PhysicalThreadID, EndOrdinalExclusive: ordinal, EndByteOffset: offset}
	default:
		return nil, invalidThreadHistory(fmt.Sprintf("unknown fork boundary %q", boundaryKind))
	}
	return normalizeThreadHistoryForkPosition(lineage, position)
}

func (r *StateRuntime) materializePaginatedLineageForReference(ctx context.Context, requestedThreadID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	requestedThreadID = strings.TrimSpace(requestedThreadID)
	seen := map[string]bool{}
	threadID := requestedThreadID
	for {
		if seen[threadID] {
			return invalidPaginatedLineage(requestedThreadID, "cycle detected")
		}
		seen[threadID] = true
		path, found, err := r.FindRolloutPathByID(ctx, threadID, nil)
		if err != nil {
			return err
		}
		if !found {
			return invalidPaginatedLineage(threadID, "missing source rollout")
		}
		existing, ok := rollout.ExistingRolloutPath(path)
		if !ok {
			return invalidPaginatedLineage(threadID, "missing source rollout")
		}
		plain, err := rollout.MaterializeRolloutForReference(existing)
		if err != nil {
			return fmt.Errorf("failed to materialize referenced rollout %s: %w", existing, err)
		}
		meta, err := rollout.FirstSessionMeta(plain)
		if err != nil {
			return fmt.Errorf("failed to read lineage metadata %s: %w", plain, err)
		}
		if meta.ID != threadID {
			return invalidPaginatedLineage(requestedThreadID, "source rollout belongs to another thread")
		}
		if !strings.EqualFold(strings.TrimSpace(meta.HistoryMode), "paginated") {
			return invalidPaginatedLineage(requestedThreadID, "source rollout is not paginated")
		}
		if meta.HistoryBase == nil {
			return nil
		}
		threadID = strings.TrimSpace(meta.HistoryBase.ThreadID)
	}
}

func findThreadHistoryForkTurn(ctx context.Context, db *sql.DB, lineage []threadHistoryLineageSegment, turnID string, newest bool) (*threadHistoryForkTurnRow, error) {
	start, end, step := 0, len(lineage), 1
	if newest {
		start, end, step = len(lineage)-1, -1, -1
	}
	for index := start; index != end; index += step {
		segment := lineage[index]
		query := `
SELECT rollout_ordinal, rollout_byte_offset, rollout_end_ordinal, rollout_end_byte_offset, status
FROM thread_turns
WHERE thread_id = ? AND turn_id = ? AND rollout_ordinal >= ?
  AND (? IS NULL OR rollout_ordinal < ?)`
		var row threadHistoryForkTurnRow
		err := db.QueryRowContext(ctx, query, segment.ThreadID, turnID, sqliteInt(segment.Start), nullableHistoryEnd(segment.End), nullableHistoryEnd(segment.End)).Scan(
			&row.RolloutOrdinal, &row.RolloutByteOffset, &row.RolloutEndOrdinal, &row.RolloutEndByteOffset, &row.Status,
		)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("resolve logical turn: %w", err)
		}
		row.PhysicalThreadID = segment.ThreadID
		return &row, nil
	}
	return nil, invalidThreadHistory("turn not found: " + turnID)
}

func normalizeThreadHistoryForkPosition(lineage []threadHistoryLineageSegment, position *rollout.HistoryPosition) (*rollout.HistoryPosition, error) {
	if position == nil {
		return nil, nil
	}
	segmentIndex := -1
	for i := range lineage {
		if lineage[i].ThreadID == position.ThreadID {
			segmentIndex = i
			break
		}
	}
	if segmentIndex < 0 {
		return nil, fmt.Errorf("fork position is outside the source lineage")
	}
	segment := lineage[segmentIndex]
	if segment.End != nil && (position.EndOrdinalExclusive > segment.End.EndOrdinalExclusive || position.EndByteOffset > segment.End.EndByteOffset) {
		return nil, invalidThreadHistory("fork boundary exceeds inherited source history")
	}
	if position.EndOrdinalExclusive == segment.Start {
		if segmentIndex == 0 {
			return nil, nil
		}
		return cloneRolloutHistoryPosition(lineage[segmentIndex-1].End), nil
	}
	return cloneRolloutHistoryPosition(position), nil
}

func nonNegativeUint64(value int64) (uint64, bool) {
	if value < 0 {
		return 0, false
	}
	return uint64(value), true
}
