package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ThreadReadState is one durable unread position. A nil FirstUnreadTurn means
// the thread is read; an empty string marks the whole thread unread, including
// threads without turns (Rust state/src/runtime/thread_read_state.rs).
type ThreadReadState struct {
	FirstUnreadTurn *string
	Revision        string
}

// ReadStateOperation is an explicit read/unread edit.
type ReadStateOperation string

const (
	ReadStateOperationRead   ReadStateOperation = "read"
	ReadStateOperationUnread ReadStateOperation = "unread"
)

// ReadStateUpdateKind classifies the result of a revision-checked edit.
type ReadStateUpdateKind string

const (
	ReadStateUpdateApplied     ReadStateUpdateKind = "applied"
	ReadStateUpdateConflict    ReadStateUpdateKind = "conflict"
	ReadStateUpdateUnavailable ReadStateUpdateKind = "unavailable"
)

// ReadStateUpdate carries the current state for applied and conflict results.
type ReadStateUpdate struct {
	Kind  ReadStateUpdateKind
	State ThreadReadState
}

type threadReadStateQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// ThreadReadStates reads receipts for the requested durable thread IDs without
// creating rows. A thread with no receipt gets a thread-bound initial revision
// so a revision captured for one thread cannot edit another.
func (r *StateRuntime) ThreadReadStates(ctx context.Context, threadIDs []string) (map[string]ThreadReadState, error) {
	if r == nil || r.stateDB == nil {
		return nil, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return readThreadReadStates(ctx, r.stateDB, threadIDs)
}

func readThreadReadStates(ctx context.Context, queryer threadReadStateQueryer, threadIDs []string) (map[string]ThreadReadState, error) {
	ids := compactThreadIDs(threadIDs)
	states := make(map[string]ThreadReadState, len(ids))
	for len(ids) > 0 {
		chunkSize := len(ids)
		if chunkSize > 998 {
			chunkSize = 998
		}
		chunk := ids[:chunkSize]
		ids = ids[chunkSize:]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i := range chunk {
			args[i] = chunk[i]
		}
		rows, err := queryer.QueryContext(ctx, `
SELECT threads.id, receipts.first_unread_turn, receipts.revision
FROM threads
LEFT JOIN thread_read_receipts AS receipts ON receipts.thread_id = threads.id
WHERE threads.id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("read thread read states: %w", err)
		}
		for rows.Next() {
			var id string
			var firstUnread sql.NullString
			var revision sql.NullString
			if err := rows.Scan(&id, &firstUnread, &revision); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan thread read state: %w", err)
			}
			var firstUnreadTurn *string
			if firstUnread.Valid {
				value := firstUnread.String
				firstUnreadTurn = &value
			}
			value := revision.String
			if !revision.Valid {
				value = "initial:" + id
			}
			states[id] = ThreadReadState{FirstUnreadTurn: firstUnreadTurn, Revision: value}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read thread read states: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close thread read states: %w", err)
		}
	}
	return states, nil
}

// PublishThreadAttention records a newly delivered terminal turn once. The
// first unread position is retained across acknowledgements, while the last
// published turn suppresses duplicate delivery after a read.
func (r *StateRuntime) PublishThreadAttention(ctx context.Context, threadID, turnID string) (bool, error) {
	if r == nil || r.stateDB == nil {
		return false, errors.New("state runtime is nil")
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return false, errors.New("thread id and turn id are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := ExecWrite(ctx, r.stateDB, `
INSERT INTO thread_read_receipts(thread_id, first_unread_turn, revision, last_published_turn)
SELECT id, ?, ?, ? FROM threads WHERE id = ?
ON CONFLICT(thread_id) DO UPDATE SET
    first_unread_turn = COALESCE(thread_read_receipts.first_unread_turn, excluded.first_unread_turn),
    revision = excluded.revision,
    last_published_turn = excluded.last_published_turn
WHERE thread_read_receipts.last_published_turn IS NOT excluded.last_published_turn`,
		turnID, uuid.NewString(), turnID, threadID)
	if err != nil {
		return false, fmt.Errorf("publish thread attention: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read thread attention update count: %w", err)
	}
	return rows > 0, nil
}

// UpdateThreadReadState applies an explicit edit only when expectedRevision
// matches the caller's snapshot. Stale edits return the current state for a
// fresh user action instead of being retried automatically.
func (r *StateRuntime) UpdateThreadReadState(ctx context.Context, threadID, expectedRevision string, operation ReadStateOperation) (ReadStateUpdate, error) {
	if r == nil || r.stateDB == nil {
		return ReadStateUpdate{}, errors.New("state runtime is nil")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ReadStateUpdate{}, errors.New("thread id is required")
	}
	switch operation {
	case ReadStateOperationRead, ReadStateOperationUnread:
	default:
		return ReadStateUpdate{}, fmt.Errorf("unknown read state operation %q", operation)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := BeginImmediateTx(ctx, r.stateDB)
	if err != nil {
		return ReadStateUpdate{}, fmt.Errorf("begin thread read state update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	states, err := readThreadReadStates(ctx, tx, []string{threadID})
	if err != nil {
		return ReadStateUpdate{}, err
	}
	before, ok := states[threadID]
	if !ok {
		return ReadStateUpdate{Kind: ReadStateUpdateUnavailable}, nil
	}
	if before.Revision != expectedRevision {
		return ReadStateUpdate{Kind: ReadStateUpdateConflict, State: before}, nil
	}
	var firstUnread *string
	if operation == ReadStateOperationUnread {
		empty := ""
		firstUnread = &empty
	}
	revision := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO thread_read_receipts(thread_id, first_unread_turn, revision)
VALUES(?, ?, ?)
ON CONFLICT(thread_id) DO UPDATE SET
    first_unread_turn = excluded.first_unread_turn,
    revision = excluded.revision`, threadID, nullableStringPointer(firstUnread), revision); err != nil {
		return ReadStateUpdate{}, fmt.Errorf("update thread read state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ReadStateUpdate{}, fmt.Errorf("commit thread read state update: %w", err)
	}
	return ReadStateUpdate{
		Kind:  ReadStateUpdateApplied,
		State: ThreadReadState{FirstUnreadTurn: firstUnread, Revision: revision},
	}, nil
}
