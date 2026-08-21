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
	ThreadID       *string                 `json:"thread_id,omitempty"`
	RolloutPath    string                  `json:"rollout_path"`
	Status         MigrationStatus         `json:"status"`
	FailureReason  *MigrationFailureReason `json:"failure_reason,omitempty"`
	BytesProcessed uint64                  `json:"bytes_processed"`
	Message        *string                 `json:"message,omitempty"`
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
		collected, err := CollectRolloutPaths(root)
		if err != nil {
			// A missing sessions directory is not an error; an unreadable one
			// surfaces in per-file outcomes only when files exist.
			if !os.IsNotExist(err) {
				return nil, err
			}
			continue
		}
		paths = append(paths, collected...)
	}
	sort.Strings(paths)
	for _, path := range paths {
		outcome, ok := inspectRolloutPath(codexHome, path, options)
		if !ok {
			continue
		}
		report.Outcomes = append(report.Outcomes, *outcome)
	}
	return report, nil
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
