package state

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	// MaxGuardianReviewRecords bounds failed-review evidence globally.
	MaxGuardianReviewRecords = 64
	// MaxGuardianReviewRecordsPerThread bounds evidence for one reviewed thread.
	MaxGuardianReviewRecordsPerThread = 8
	// MaxGuardianReviewBytes bounds serialized evidence, counting one byte per
	// record separator.
	MaxGuardianReviewBytes = 8 * 1024 * 1024
)

// GuardianReviewRecord is one complete serialized failed-review record. IDs are
// UUIDv7 values so storage order reflects capture order.
type GuardianReviewRecord struct {
	ID       string
	ThreadID string
	Record   []byte
}

// NewGuardianReviewRecord creates an ordered record for a persisted thread.
func NewGuardianReviewRecord(threadID string, record []byte) GuardianReviewRecord {
	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New()
	}
	return GuardianReviewRecord{
		ID:       id.String(),
		ThreadID: strings.TrimSpace(threadID),
		Record:   append([]byte(nil), record...),
	}
}

// RecordGuardianReviewFailure inserts evidence and evicts older records under
// the global/per-thread count and byte budgets in one immediate transaction.
func (r *StateRuntime) RecordGuardianReviewFailure(ctx context.Context, record *GuardianReviewRecord) error {
	if r == nil || r.stateDB == nil {
		return errors.New("state runtime is nil")
	}
	if record == nil {
		return errors.New("guardian review record is nil")
	}
	threadID := strings.TrimSpace(record.ThreadID)
	if threadID == "" || strings.TrimSpace(record.ID) == "" {
		return errors.New("guardian review record id and thread id are required")
	}
	if len(record.Record) >= MaxGuardianReviewBytes {
		return errors.New("Guardian feedback record exceeds its size limit")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := BeginImmediateTx(ctx, r.stateDB)
	if err != nil {
		return fmt.Errorf("begin guardian review feedback write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO guardian_review_feedback (id, thread_id, record) VALUES (?, ?, ?)`,
		record.ID, threadID, record.Record); err != nil {
		return fmt.Errorf("insert guardian review feedback: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM guardian_review_feedback WHERE id IN (
    SELECT id FROM guardian_review_feedback
    WHERE thread_id = ?
    ORDER BY id DESC
    LIMIT -1 OFFSET ?
)`, threadID, MaxGuardianReviewRecordsPerThread); err != nil {
		return fmt.Errorf("trim guardian review feedback for thread: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM guardian_review_feedback WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (ORDER BY id DESC) AS n,
               SUM(length(record) + 1) OVER (ORDER BY id DESC) AS bytes
        FROM guardian_review_feedback
    )
    WHERE n > ? OR bytes > ?
)`, MaxGuardianReviewRecords, MaxGuardianReviewBytes); err != nil {
		return fmt.Errorf("trim guardian review feedback budget: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit guardian review feedback: %w", err)
	}
	return nil
}

// ListGuardianReviewRecords returns retained evidence oldest first. Callers must
// filter it to the reported task tree.
func (r *StateRuntime) ListGuardianReviewRecords(ctx context.Context) ([]GuardianReviewRecord, error) {
	if r == nil || r.stateDB == nil {
		return nil, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := r.stateDB.QueryContext(ctx, `
SELECT id, thread_id, record
FROM guardian_review_feedback
ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list guardian review feedback: %w", err)
	}
	defer rows.Close()
	records := []GuardianReviewRecord{}
	for rows.Next() {
		var record GuardianReviewRecord
		if err := rows.Scan(&record.ID, &record.ThreadID, &record.Record); err != nil {
			return nil, fmt.Errorf("scan guardian review feedback: %w", err)
		}
		record.Record = append([]byte(nil), record.Record...)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate guardian review feedback: %w", err)
	}
	return records, nil
}
