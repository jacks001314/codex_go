package rollout

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MigrationStatus mirrors Rust thread-store/src/local/rollout_migration.rs
// RolloutMigrationStatus (serde snake_case).
type MigrationStatus string

const (
	MigrationStatusEligible         MigrationStatus = "eligible"
	MigrationStatusMigrated         MigrationStatus = "migrated"
	MigrationStatusAlreadyPaginated MigrationStatus = "already_paginated"
	MigrationStatusSkippedEmpty     MigrationStatus = "skipped_empty"
	MigrationStatusSkippedBusy      MigrationStatus = "skipped_busy"
	MigrationStatusFailed           MigrationStatus = "failed"
)

// MigrationFailureReason mirrors Rust RolloutMigrationFailureReason (#39784):
// a bounded explanation for why one rollout migration failed (serde snake_case).
type MigrationFailureReason string

const (
	MigrationFailureMissingSqliteMetadata            MigrationFailureReason = "missing_sqlite_metadata"
	MigrationFailureInvalidSessionMetadata           MigrationFailureReason = "invalid_session_metadata"
	MigrationFailureRolloutReadFailed                MigrationFailureReason = "rollout_read_failed"
	MigrationFailureLegacyRolloutConversionFailed    MigrationFailureReason = "legacy_rollout_conversion_failed"
	MigrationFailureSQLiteMaterializationFailed      MigrationFailureReason = "sqlite_materialization_failed"
	MigrationFailureRolloutPublishFailed             MigrationFailureReason = "rollout_publish_failed"
	MigrationFailureInterruptedMigrationRecoveryFail MigrationFailureReason = "interrupted_migration_recovery_failed"
	MigrationFailureUnknown                          MigrationFailureReason = "unknown"
)

// MigrationOutcome mirrors Rust RolloutMigrationOutcome.
type MigrationOutcome struct {
	ThreadID       *string                 `json:"thread_id"`
	RolloutPath    string                  `json:"rollout_path"`
	Status         MigrationStatus         `json:"status"`
	FailureReason  *MigrationFailureReason `json:"failure_reason,omitempty"`
	BytesProcessed uint64                  `json:"bytes_processed"`
	Message        *string                 `json:"message"`
}

// MigrationReport mirrors Rust RolloutMigrationReport.
type MigrationReport struct {
	Outcomes []MigrationOutcome `json:"outcomes"`
}

// MigrationOptions mirrors Rust RolloutMigrationOptions.
type MigrationOptions struct {
	Apply           bool
	ThreadIDs       []string
	MaxMibPerSecond uint64
	// Progress receives one update per inspected path, mirroring Rust
	// LocalThreadStore::migrate_rollouts_with_progress. OutcomeStatus is nil for
	// paths skipped before inspection because they belong to an unselected thread.
	Progress func(MigrationProgress)
	// IndexedRollouts is the state database's thread-to-rollout index. Migrating
	// selected threads uses it to skip rollout headers that provably belong to
	// unrelated threads (Rust #57483). It stays empty when the index is
	// unavailable, and then every candidate header is inspected.
	IndexedRollouts []MigrationIndexedRollout
}

// MigrationProgress mirrors Rust thread-store RolloutMigrationProgress.
type MigrationProgress struct {
	ProcessedPaths int
	TotalPaths     int
	OutcomeStatus  *MigrationStatus
}

// MigrationIndexedRollout is one state-database index row: the thread that owns
// the indexed rollout path.
type MigrationIndexedRollout struct {
	ThreadID    string
	RolloutPath string
}

// MigrateRollouts scans sessions and archived_sessions for rollout files and
// reports or migrates them, mirroring Rust LocalThreadStore::migrate_rollouts.
// The dry-run mode (Apply=false) only inspects files and reports their
// eligibility; it never modifies local storage.
func MigrateRollouts(codexHome string, options MigrationOptions) (*MigrationReport, error) {
	report := &MigrationReport{Outcomes: []MigrationOutcome{}}
	if options.Apply {
		// Mirrors Rust: migration replaces rollout files by rename; exclude
		// rollout compression and concurrent migration for this Codex home
		// with the process-scoped nonblocking maintenance lock.
		guard, err := TryAcquireRolloutMaintenanceLock(codexHome)
		if err != nil {
			return nil, err
		}
		if guard == nil {
			// Another maintenance job holds the lock; report eligible files as
			// busy instead of racing the rename.
			report.Outcomes = append(report.Outcomes, *migrationOutcome(nil, filepath.Join(codexHome, "sessions"), MigrationStatusSkippedBusy, 0, "another rollout maintenance job is running; retry later"))
			return report, nil
		}
		defer guard.Release()
	}
	roots := []string{
		filepath.Join(codexHome, SessionsSubdir),
		filepath.Join(codexHome, ArchivedSessionsSubdir),
	}
	var paths []string
	for _, root := range roots {
		collected, err := migrationRolloutPaths(root)
		if err != nil {
			// A missing sessions directory is not an error; an unreadable one
			// surfaces in per-file outcomes only when files exist.
			if !os.IsNotExist(err) {
				return nil, err
			}
			continue
		}
		// Rust sorts each root descending (newest rollout first) and keeps the
		// active tree ahead of the archived one.
		sort.Sort(sort.Reverse(sort.StringSlice(collected)))
		paths = append(paths, collected...)
	}
	if len(options.ThreadIDs) > 0 {
		paths = pruneUnselectedIndexedRolloutPaths(paths, options)
	}
	totalPaths := len(paths)
	for index, path := range paths {
		outcome, ok := inspectRolloutPath(codexHome, path, options)
		if ok {
			report.Outcomes = append(report.Outcomes, *outcome)
		}
		if options.Progress != nil {
			update := MigrationProgress{ProcessedPaths: index + 1, TotalPaths: totalPaths}
			if ok {
				status := outcome.Status
				update.OutcomeStatus = &status
			}
			options.Progress(update)
		}
	}
	return report, nil
}

// migrationRolloutPaths collects the rollout files Rust migration inspects: the
// names starting with "rollout-" and ending in .jsonl or .jsonl.zst, skipping a
// compressed file that already has its plain counterpart.
func migrationRolloutPaths(root string) ([]string, error) {
	collected, err := CollectRolloutPaths(root)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(collected))
	for _, path := range collected {
		name := filepath.Base(path)
		if !strings.HasPrefix(name, "rollout-") {
			continue
		}
		switch {
		case strings.HasSuffix(name, ".jsonl"):
			paths = append(paths, path)
		case strings.HasSuffix(name, ".jsonl.zst"):
			if _, statErr := os.Stat(strings.TrimSuffix(path, ".zst")); statErr == nil {
				continue
			}
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func inspectRolloutPath(codexHome string, path string, options MigrationOptions) (*MigrationOutcome, bool) {
	meta, err := FirstSessionMeta(path)
	if err != nil {
		threadID := threadIDFromRolloutFilename(path)
		if !matchesMigrationSelection(options.ThreadIDs, threadID) {
			return nil, false
		}
		empty := false
		if info, statErr := os.Stat(path); statErr == nil && info.Size() == 0 {
			empty = true
		}
		status := MigrationStatusFailed
		var failureReason *MigrationFailureReason
		if empty {
			status = MigrationStatusSkippedEmpty
		} else {
			failureReason = classifyMigrationReadFailure(err)
		}
		message := ""
		if !empty {
			message = err.Error()
		}
		return migrationOutcomeWithReason(&threadID, path, status, failureReason, 0, message), true
	}
	threadID := strings.TrimSpace(meta.ID)
	if !matchesMigrationSelection(options.ThreadIDs, threadID) {
		return nil, false
	}
	if strings.EqualFold(strings.TrimSpace(meta.HistoryMode), "paginated") {
		return migrationOutcome(&threadID, path, MigrationStatusAlreadyPaginated, 0, ""), true
	}
	if !options.Apply {
		return migrationOutcome(&threadID, path, MigrationStatusEligible, 0, ""), true
	}
	if err := CanonicalizeRollout(codexHome, path); err != nil {
		return migrationOutcomeWithReason(&threadID, path, MigrationStatusFailed, migrationFailureReasonPtr(MigrationFailureUnknown), 0, err.Error()), true
	}
	return migrationOutcome(&threadID, path, MigrationStatusMigrated, 0, ""), true
}

// classifyMigrationReadFailure mirrors Rust #39784: filesystem read failures
// classify as rollout_read_failed; a readable file whose session metadata is
// missing/malformed is invalid_session_metadata.
func classifyMigrationReadFailure(err error) *MigrationFailureReason {
	var pathErr *os.PathError
	var linkErr *os.LinkError
	if errors.As(err, &pathErr) || errors.As(err, &linkErr) {
		return migrationFailureReasonPtr(MigrationFailureRolloutReadFailed)
	}
	return migrationFailureReasonPtr(MigrationFailureInvalidSessionMetadata)
}

func migrationOutcome(threadID *string, path string, status MigrationStatus, bytes uint64, message string) *MigrationOutcome {
	return migrationOutcomeWithReason(threadID, path, status, nil, bytes, message)
}

func migrationOutcomeWithReason(threadID *string, path string, status MigrationStatus, failureReason *MigrationFailureReason, bytes uint64, message string) *MigrationOutcome {
	outcome := &MigrationOutcome{
		RolloutPath:    path,
		Status:         status,
		FailureReason:  failureReason,
		BytesProcessed: bytes,
	}
	if threadID != nil && strings.TrimSpace(*threadID) != "" {
		id := strings.TrimSpace(*threadID)
		outcome.ThreadID = &id
	}
	if strings.TrimSpace(message) != "" {
		msg := strings.TrimSpace(message)
		outcome.Message = &msg
	}
	return outcome
}

func migrationFailureReasonPtr(reason MigrationFailureReason) *MigrationFailureReason {
	value := reason
	return &value
}

// pruneUnselectedIndexedRolloutPaths drops candidate rollouts the state index
// proves belong to an unselected thread (Rust #57483). A rollout is dropped only
// when its indexed owner and its canonical filename agree that it is
// unselected; selected, misnamed, conflicting and unindexed paths stay so the
// rollout header still decides ownership.
func pruneUnselectedIndexedRolloutPaths(paths []string, options MigrationOptions) []string {
	if len(options.IndexedRollouts) == 0 || len(options.ThreadIDs) == 0 {
		return paths
	}
	selected := make(map[string]bool, len(options.ThreadIDs))
	for _, threadID := range options.ThreadIDs {
		selected[strings.TrimSpace(threadID)] = true
	}
	needsHeader := make(map[string]bool, len(options.IndexedRollouts))
	for _, indexed := range options.IndexedRollouts {
		owner := strings.TrimSpace(indexed.ThreadID)
		indexedPath := strings.TrimSpace(indexed.RolloutPath)
		if owner == "" || indexedPath == "" {
			continue
		}
		filenameThreadID, ok := ThreadIDFromRolloutPath(indexedPath)
		inspect := !ok || selected[owner] || filenameThreadID != owner
		// Conflicting rows can name the same plain or compressed file.
		plain := PlainRolloutPath(indexedPath)
		if _, seen := needsHeader[plain]; seen {
			inspect = true
		}
		needsHeader[plain] = inspect
	}
	pruned := paths[:0]
	for _, path := range paths {
		inspect, indexed := needsHeader[PlainRolloutPath(path)]
		if indexed && !inspect {
			continue
		}
		pruned = append(pruned, path)
	}
	return pruned
}

func matchesMigrationSelection(selected []string, threadID string) bool {
	if len(selected) == 0 {
		return true
	}
	for _, id := range selected {
		if strings.TrimSpace(id) == strings.TrimSpace(threadID) {
			return true
		}
	}
	return false
}

func threadIDFromRolloutFilename(path string) string {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, ".jsonl")
	name = strings.TrimSuffix(name, ".jsonl.zst")
	// Mirrors Rust thread_id_from_rollout_filename: the rollout file stem ends
	// with the 36-character thread id (UUID); fall back to the whole stem when
	// the id cannot be recovered.
	if len(name) >= 36 {
		return name[len(name)-36:]
	}
	return name
}

// RenderJSONReport serializes the report in the Rust JSON shape.
func RenderJSONReport(report *MigrationReport) ([]byte, error) {
	if report == nil {
		report = &MigrationReport{Outcomes: []MigrationOutcome{}}
	}
	if report.Outcomes == nil {
		report.Outcomes = []MigrationOutcome{}
	}
	return json.MarshalIndent(report, "", "  ")
}

// FormatOutcomeStatus renders one outcome line like Rust print_outcome.
func FormatOutcomeStatus(status MigrationStatus) string {
	switch status {
	case MigrationStatusEligible:
		return "eligible"
	case MigrationStatusMigrated:
		return "migrated"
	case MigrationStatusAlreadyPaginated:
		return "already paginated"
	case MigrationStatusSkippedEmpty:
		return "skipped empty"
	case MigrationStatusSkippedBusy:
		return "skipped busy"
	case MigrationStatusFailed:
		return "failed"
	default:
		return string(status)
	}
}
