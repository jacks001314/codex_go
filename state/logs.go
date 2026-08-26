package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	logPartitionSizeLimitBytes int64 = 10 * 1024 * 1024
	logPartitionRowLimit       int64 = 1_000
	logRetentionDays                 = 10
)

// logBatchByteBoundaries are the explicit byte histogram bucket boundaries for
// SQLite log batch metrics (Rust #40726).
var logBatchByteBoundaries = []float64{256, 1024, 4096, 16384, 65536, 262144, 1048576}

type LogEntry struct {
	TS              int64
	TSNanos         int64
	Level           string
	Target          string
	Message         *string
	FeedbackLogBody *string
	ThreadID        *string
	ProcessUUID     *string
	ModulePath      *string
	File            *string
	Line            *int64
}

type LogRow struct {
	ID          int64
	TS          int64
	TSNanos     int64
	Level       string
	Target      string
	Message     *string
	ThreadID    *string
	ProcessUUID *string
	File        *string
	Line        *int64
}

type LogQuery struct {
	LevelsUpper       []string
	FromTS            *int64
	ToTS              *int64
	ModuleLike        []string
	FileLike          []string
	ThreadIDs         []string
	Search            *string
	IncludeThreadless bool
	AfterID           *int64
	Limit             *int64
	Descending        bool
}

func (r *StateRuntime) InsertLog(ctx context.Context, entry LogEntry) error {
	return r.InsertLogs(ctx, []LogEntry{entry})
}

// InsertLogs writes and prunes one batch atomically.
func (r *StateRuntime) InsertLogs(ctx context.Context, entries []LogEntry) (err error) {
	if err := r.requireLogsDB(); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	started := time.Now()
	defer func() {
		if r.metrics != nil {
			status := "ok"
			if err != nil {
				status = "error"
			}
			r.metrics.Counter("codex.sqlite.log.write", 1, map[string]string{"status": status})
			r.metrics.Histogram("codex.sqlite.log.write.duration_ms", int(time.Since(started).Milliseconds()), nil)
			r.metrics.Counter("codex.sqlite.log.write.entries", len(entries), nil)
			batchBytes := int64(0)
			largest := int64(0)
			for i := range entries {
				entryBytes := int64(len(entries[i].Level) + len(entries[i].Target))
				if entries[i].FeedbackLogBody != nil {
					entryBytes += int64(len(*entries[i].FeedbackLogBody))
				}
				if entries[i].Message != nil {
					entryBytes += int64(len(*entries[i].Message))
				}
				batchBytes += entryBytes
				if entryBytes > largest {
					largest = entryBytes
				}
			}
			r.metrics.HistogramWithBounds("codex.sqlite.log.write.batch_bytes", int(batchBytes), logBatchByteBoundaries, nil)
			r.metrics.HistogramWithBounds("codex.sqlite.log.write.largest_entry_bytes", int(largest), logBatchByteBoundaries, nil)
		}
	}()
	ctx = nonNilContext(ctx)
	tx, err := r.logsDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin log insert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO logs (
    ts, ts_nanos, level, target, feedback_log_body, thread_id,
    process_uuid, module_path, file, line, estimated_bytes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare log insert: %w", err)
	}
	for i := range entries {
		entry := &entries[i]
		body := entry.FeedbackLogBody
		if body == nil {
			body = entry.Message
		}
		estimatedBytes := int64(len(entry.Level) + len(entry.Target))
		if body != nil {
			estimatedBytes += int64(len(*body))
		}
		if entry.ModulePath != nil {
			estimatedBytes += int64(len(*entry.ModulePath))
		}
		if entry.File != nil {
			estimatedBytes += int64(len(*entry.File))
		}
		if _, err := stmt.ExecContext(ctx, entry.TS, entry.TSNanos, entry.Level, entry.Target, body, entry.ThreadID, entry.ProcessUUID, entry.ModulePath, entry.File, entry.Line, estimatedBytes); err != nil {
			_ = stmt.Close()
			return fmt.Errorf("insert log entry %d: %w", i, err)
		}
	}
	if err := stmt.Close(); err != nil {
		return fmt.Errorf("close log insert statement: %w", err)
	}
	if err := pruneLogsAfterInsert(ctx, tx, entries); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit log insert: %w", err)
	}
	return nil
}

func pruneLogsAfterInsert(ctx context.Context, tx *sql.Tx, entries []LogEntry) error {
	threadIDs := make(map[string]struct{})
	processUUIDs := make(map[string]struct{})
	hasNullProcess := false
	for i := range entries {
		entry := &entries[i]
		if entry.ThreadID != nil {
			threadIDs[*entry.ThreadID] = struct{}{}
			continue
		}
		if entry.ProcessUUID == nil {
			hasNullProcess = true
		} else {
			processUUIDs[*entry.ProcessUUID] = struct{}{}
		}
	}
	for _, threadID := range sortedSetKeys(threadIDs) {
		if err := pruneLogPartition(ctx, tx, `thread_id = ?`, []any{threadID}); err != nil {
			return fmt.Errorf("prune logs for thread %s: %w", threadID, err)
		}
	}
	for _, processUUID := range sortedSetKeys(processUUIDs) {
		if err := pruneLogPartition(ctx, tx, `thread_id IS NULL AND process_uuid = ?`, []any{processUUID}); err != nil {
			return fmt.Errorf("prune threadless logs for process %s: %w", processUUID, err)
		}
	}
	if hasNullProcess {
		if err := pruneLogPartition(ctx, tx, `thread_id IS NULL AND process_uuid IS NULL`, nil); err != nil {
			return fmt.Errorf("prune threadless logs without process uuid: %w", err)
		}
	}
	return nil
}

func pruneLogPartition(ctx context.Context, tx *sql.Tx, predicate string, predicateArgs []any) error {
	var totalBytes, rowCount int64
	usageQuery := `SELECT COALESCE(SUM(estimated_bytes), 0), COUNT(*) FROM logs WHERE ` + predicate
	if err := tx.QueryRowContext(ctx, usageQuery, predicateArgs...).Scan(&totalBytes, &rowCount); err != nil {
		return err
	}
	if totalBytes <= logPartitionSizeLimitBytes && rowCount <= logPartitionRowLimit {
		return nil
	}
	query := `
DELETE FROM logs
WHERE id IN (
    SELECT id
    FROM (
        SELECT
            id,
            SUM(estimated_bytes) OVER (
                ORDER BY ts DESC, ts_nanos DESC, id DESC
            ) AS cumulative_bytes,
            ROW_NUMBER() OVER (
                ORDER BY ts DESC, ts_nanos DESC, id DESC
            ) AS row_number
        FROM logs
        WHERE ` + predicate + `
    )
    WHERE cumulative_bytes > ? OR row_number > ?
)`
	args := append([]any(nil), predicateArgs...)
	args = append(args, logPartitionSizeLimitBytes, logPartitionRowLimit)
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (r *StateRuntime) DeleteLogsBefore(ctx context.Context, cutoffTS int64) (int64, error) {
	if err := r.requireLogsDB(); err != nil {
		return 0, err
	}
	result, err := r.logsDB.ExecContext(nonNilContext(ctx), `DELETE FROM logs WHERE ts < ?`, cutoffTS)
	if err != nil {
		return 0, fmt.Errorf("delete logs before %d: %w", cutoffTS, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read deleted log count: %w", err)
	}
	return rows, nil
}

func (r *StateRuntime) runLogsStartupMaintenance(ctx context.Context, now time.Time) error {
	cutoff := now.UTC().AddDate(0, 0, -logRetentionDays).Unix()
	if _, err := r.DeleteLogsBefore(ctx, cutoff); err != nil {
		return err
	}
	if _, err := r.logsDB.ExecContext(nonNilContext(ctx), `PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
		return fmt.Errorf("checkpoint logs database: %w", err)
	}
	return nil
}

func (r *StateRuntime) QueryLogs(ctx context.Context, query LogQuery) ([]LogRow, error) {
	if err := r.requireLogsDB(); err != nil {
		return nil, err
	}
	where, args := buildLogFilters(query)
	statement := `SELECT id, ts, ts_nanos, level, target, feedback_log_body, thread_id, process_uuid, file, line FROM logs WHERE 1 = 1` + where
	if query.Descending {
		statement += ` ORDER BY id DESC`
	} else {
		statement += ` ORDER BY id ASC`
	}
	if query.Limit != nil {
		statement += ` LIMIT ?`
		args = append(args, *query.Limit)
	}
	rows, err := r.logsDB.QueryContext(nonNilContext(ctx), statement, args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()
	result := make([]LogRow, 0)
	for rows.Next() {
		var row LogRow
		var message, threadID, processUUID, file sql.NullString
		var line sql.NullInt64
		if err := rows.Scan(&row.ID, &row.TS, &row.TSNanos, &row.Level, &row.Target, &message, &threadID, &processUUID, &file, &line); err != nil {
			return nil, fmt.Errorf("scan log row: %w", err)
		}
		row.Message = nullStringPointer(message)
		row.ThreadID = nullStringPointer(threadID)
		row.ProcessUUID = nullStringPointer(processUUID)
		row.File = nullStringPointer(file)
		if line.Valid {
			value := line.Int64
			row.Line = &value
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate log rows: %w", err)
	}
	return result, nil
}

func (r *StateRuntime) MaxLogID(ctx context.Context, query LogQuery) (int64, error) {
	if err := r.requireLogsDB(); err != nil {
		return 0, err
	}
	where, args := buildLogFilters(query)
	var maxID sql.NullInt64
	if err := r.logsDB.QueryRowContext(nonNilContext(ctx), `SELECT MAX(id) FROM logs WHERE 1 = 1`+where, args...).Scan(&maxID); err != nil {
		return 0, fmt.Errorf("query max log id: %w", err)
	}
	if !maxID.Valid {
		return 0, nil
	}
	return maxID.Int64, nil
}

func buildLogFilters(query LogQuery) (string, []any) {
	var where strings.Builder
	args := make([]any, 0)
	if len(query.LevelsUpper) > 0 {
		where.WriteString(` AND UPPER(level) IN (`)
		writePlaceholders(&where, len(query.LevelsUpper))
		where.WriteByte(')')
		for _, level := range query.LevelsUpper {
			args = append(args, level)
		}
	}
	if query.FromTS != nil {
		where.WriteString(` AND ts >= ?`)
		args = append(args, *query.FromTS)
	}
	if query.ToTS != nil {
		where.WriteString(` AND ts <= ?`)
		args = append(args, *query.ToTS)
	}
	appendLikeFilters(&where, &args, "module_path", query.ModuleLike)
	appendLikeFilters(&where, &args, "file", query.FileLike)
	if len(query.ThreadIDs) > 0 || query.IncludeThreadless {
		where.WriteString(` AND (`)
		needsOr := false
		for _, threadID := range query.ThreadIDs {
			if needsOr {
				where.WriteString(` OR `)
			}
			where.WriteString(`thread_id = ?`)
			args = append(args, threadID)
			needsOr = true
		}
		if query.IncludeThreadless {
			if needsOr {
				where.WriteString(` OR `)
			}
			where.WriteString(`thread_id IS NULL`)
		}
		where.WriteByte(')')
	}
	if query.AfterID != nil {
		where.WriteString(` AND id > ?`)
		args = append(args, *query.AfterID)
	}
	if query.Search != nil {
		where.WriteString(` AND INSTR(COALESCE(feedback_log_body, ''), ?) > 0`)
		args = append(args, *query.Search)
	}
	return where.String(), args
}

func appendLikeFilters(where *strings.Builder, args *[]any, column string, filters []string) {
	if len(filters) == 0 {
		return
	}
	where.WriteString(` AND (`)
	for i, filter := range filters {
		if i > 0 {
			where.WriteString(` OR `)
		}
		where.WriteString(column)
		where.WriteString(` LIKE '%' || ? || '%'`)
		*args = append(*args, filter)
	}
	where.WriteByte(')')
}

func (r *StateRuntime) QueryFeedbackLogs(ctx context.Context, threadID string) ([]byte, error) {
	return r.QueryFeedbackLogsForThreads(ctx, []string{threadID})
}

func (r *StateRuntime) QueryFeedbackLogsForThreads(ctx context.Context, threadIDs []string) ([]byte, error) {
	if err := r.requireLogsDB(); err != nil {
		return nil, err
	}
	threadIDs = uniqueStrings(threadIDs)
	if len(threadIDs) == 0 {
		return []byte{}, nil
	}
	ctx = nonNilContext(ctx)
	processUUIDs := make(map[string]struct{})
	for _, threadID := range threadIDs {
		var processUUID sql.NullString
		err := r.logsDB.QueryRowContext(ctx, `
SELECT process_uuid
FROM logs
WHERE thread_id = ? AND process_uuid IS NOT NULL
ORDER BY ts DESC, ts_nanos DESC, id DESC
LIMIT 1`, threadID).Scan(&processUUID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("query latest log process for thread %s: %w", threadID, err)
		}
		if processUUID.Valid {
			processUUIDs[processUUID.String] = struct{}{}
		}
	}
	var statement strings.Builder
	statement.WriteString(`SELECT ts, ts_nanos, level, feedback_log_body, estimated_bytes FROM logs WHERE feedback_log_body IS NOT NULL AND (thread_id IN (`)
	writePlaceholders(&statement, len(threadIDs))
	statement.WriteByte(')')
	args := make([]any, 0, len(threadIDs)+len(processUUIDs))
	for _, threadID := range threadIDs {
		args = append(args, threadID)
	}
	processes := sortedSetKeys(processUUIDs)
	if len(processes) > 0 {
		statement.WriteString(` OR (thread_id IS NULL AND process_uuid IN (`)
		writePlaceholders(&statement, len(processes))
		statement.WriteString(`))`)
		for _, processUUID := range processes {
			args = append(args, processUUID)
		}
	}
	statement.WriteString(`) ORDER BY ts DESC, ts_nanos DESC, id DESC`)
	rows, err := r.logsDB.QueryContext(ctx, statement.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query feedback logs: %w", err)
	}
	defer rows.Close()
	lines := make([]string, 0)
	var cumulativeEstimate, formattedBytes int64
	for rows.Next() {
		var ts, tsNanos, estimatedBytes int64
		var level, body string
		if err := rows.Scan(&ts, &tsNanos, &level, &body, &estimatedBytes); err != nil {
			return nil, fmt.Errorf("scan feedback log: %w", err)
		}
		cumulativeEstimate += estimatedBytes
		if cumulativeEstimate > logPartitionSizeLimitBytes {
			break
		}
		line := formatFeedbackLogLine(ts, tsNanos, level, body)
		if formattedBytes+int64(len(line)) > logPartitionSizeLimitBytes {
			break
		}
		formattedBytes += int64(len(line))
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feedback logs: %w", err)
	}
	var result strings.Builder
	result.Grow(int(formattedBytes))
	for i := len(lines) - 1; i >= 0; i-- {
		result.WriteString(lines[i])
	}
	return []byte(result.String()), nil
}

func formatFeedbackLogLine(ts int64, tsNanos int64, level string, body string) string {
	timestamp := fmt.Sprintf("%d.%09dZ", ts, tsNanos)
	if tsNanos >= 0 && tsNanos <= 999_999_999 {
		value := time.Unix(ts, tsNanos).UTC()
		if value.Year() >= 0 && value.Year() <= 9999 {
			timestamp = value.Format("2006-01-02T15:04:05.000000Z")
		}
	}
	line := fmt.Sprintf("%s %5s %s", timestamp, level, body)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	return line
}

func (r *StateRuntime) requireLogsDB() error {
	if r == nil || r.logsDB == nil {
		return errors.New("state runtime is nil")
	}
	return nil
}

func writePlaceholders(builder *strings.Builder, count int) {
	for i := 0; i < count; i++ {
		if i > 0 {
			builder.WriteString(`, `)
		}
		builder.WriteByte('?')
	}
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	copy := value.String
	return &copy
}

func sortedSetKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
