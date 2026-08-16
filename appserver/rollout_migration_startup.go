package appserver

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/features"
	"codex_go/rollout"
	"codex_go/state"
)

// legacyToPaginatedMigrationID mirrors Rust startup.rs
// LEGACY_TO_PAGINATED_MIGRATION_ID.
const legacyToPaginatedMigrationID = "legacy_to_paginated_v1"

const (
	emptySkipReason            = "empty"
	malformedSessionMetaReason = "malformed_session_meta"
	cursorLookbackSeconds      = int64(48 * 60 * 60)
)

// MaybeMigrateRolloutsOnStartup mirrors Rust thread_manager.rs: when the
// background_paginated_rollout_migration feature is enabled and a state DB is
// present, inspect rollouts on startup and migrate legacy ones in the
// background. The cursor keeps later startups cheap by only checking rollouts
// newer than the last checked frontier.
func MaybeMigrateRolloutsOnStartup(codexHome string, runtime *state.StateRuntime, cfg *config.ConfigService) {
	if runtime == nil {
		return
	}
	enabled := false
	if cfg != nil {
		if read, err := cfg.Read(&config.ConfigReadParams{}); err == nil && read != nil && read.Config != nil {
			enabled = features.Enabled((&config.Config{Values: read.Config}).FeatureSettings(), "background_paginated_rollout_migration")
		}
	}
	if !enabled {
		return
	}
	go func() {
		if err := migrateRolloutsOnStartup(context.Background(), codexHome, runtime); err != nil {
			slog.Warn("failed to migrate legacy rollouts on startup", "error", err)
		}
	}()
}

// migrateRolloutsOnStartup mirrors Rust startup.rs migrate_rollouts_on_startup.
func migrateRolloutsOnStartup(ctx context.Context, codexHome string, runtime *state.StateRuntime) error {
	paths, err := findAllRolloutPaths(codexHome)
	if err != nil {
		return err
	}
	skipped, err := state.ListRolloutMigrationSkippedRollouts(ctx, runtime.StateDB(), legacyToPaginatedMigrationID)
	if err != nil {
		return err
	}
	if hasPendingMigrationThreads(codexHome) {
		return migrateAllRolloutsOnStartup(ctx, codexHome, runtime, paths, skipped)
	}
	unchangedSkips, invalidatedSkip := revalidateSkippedRollouts(codexHome, skipped)
	migrationState, err := state.GetRolloutMigrationState(ctx, runtime.StateDB(), legacyToPaginatedMigrationID)
	if err != nil {
		return err
	}
	if migrationState == nil || invalidatedSkip {
		return migrateAllRolloutsOnStartup(ctx, codexHome, runtime, paths, skipped)
	}

	lookbackCreatedAt := int64(0)
	if migrationState.LastCheckedThread != nil {
		lookbackCreatedAt = migrationState.LastCheckedThread.ThreadCreatedAt - cursorLookbackSeconds
	}
	candidates := make([]string, 0, len(paths))
	for _, path := range paths {
		relative := relativeRolloutPath(codexHome, path)
		if unchangedSkips[relative] {
			continue
		}
		cursor := threadCreationCursor(path)
		if cursor == nil || cursor.ThreadCreatedAt < lookbackCreatedAt {
			continue
		}
		candidates = append(candidates, path)
	}
	if len(candidates) == 0 {
		return nil
	}

	unresolved := false
	for _, path := range candidates {
		inspection, err := inspectStartupRolloutPath(ctx, codexHome, runtime, path)
		if err != nil {
			return err
		}
		switch inspection {
		case startupInspectionPaginated, startupInspectionSkipped:
		case startupInspectionLegacy:
			return migrateAllRolloutsOnStartup(ctx, codexHome, runtime, paths, skipped)
		case startupInspectionUnresolved:
			unresolved = true
		}
	}
	if unresolved {
		return nil
	}
	return advanceLastCheckedThread(ctx, runtime, paths)
}

func migrateAllRolloutsOnStartup(ctx context.Context, codexHome string, runtime *state.StateRuntime, paths []string, existingSkips []state.RolloutMigrationSkippedRollout) error {
	report, err := rollout.MigrateRollouts(codexHome, rollout.MigrationOptions{Apply: true})
	if err != nil {
		return err
	}
	existingSkipPaths := map[string]bool{}
	for _, skipped := range existingSkips {
		existingSkipPaths[skipped.RolloutPath] = true
	}
	terminal := true
	reportedPaths := map[string]bool{}
	for i := range report.Outcomes {
		outcome := &report.Outcomes[i]
		relative := relativeRolloutPath(codexHome, outcome.RolloutPath)
		reportedPaths[relative] = true
		switch outcome.Status {
		case rollout.MigrationStatusMigrated, rollout.MigrationStatusAlreadyPaginated:
			if existingSkipPaths[relative] {
				if err := state.RemoveRolloutMigrationSkip(ctx, runtime.StateDB(), legacyToPaginatedMigrationID, relative); err != nil {
					return err
				}
			}
		case rollout.MigrationStatusSkippedEmpty, rollout.MigrationStatusFailed:
			if err := recordSkipIfMalformed(ctx, runtime, codexHome, outcome); err != nil {
				return err
			}
			terminal = false
		case rollout.MigrationStatusEligible, rollout.MigrationStatusSkippedBusy:
			terminal = false
		}
	}
	if !terminal {
		return nil
	}
	for _, skipped := range existingSkips {
		if !reportedPaths[skipped.RolloutPath] {
			if err := state.RemoveRolloutMigrationSkip(ctx, runtime.StateDB(), legacyToPaginatedMigrationID, skipped.RolloutPath); err != nil {
				return err
			}
		}
	}
	return advanceLastCheckedThread(ctx, runtime, paths)
}

type startupInspection int

const (
	startupInspectionPaginated startupInspection = iota
	startupInspectionLegacy
	startupInspectionSkipped
	startupInspectionUnresolved
)

func inspectStartupRolloutPath(ctx context.Context, codexHome string, runtime *state.StateRuntime, path string) (startupInspection, error) {
	before, err := rolloutFingerprint(path)
	if err != nil {
		return startupInspectionUnresolved, nil
	}
	meta, err := rollout.FirstSessionMeta(path)
	if err == nil {
		if !strings.EqualFold(strings.TrimSpace(meta.HistoryMode), "paginated") {
			return startupInspectionLegacy, nil
		}
		return startupInspectionPaginated, nil
	}
	after, fingerprintErr := rolloutFingerprint(path)
	if fingerprintErr != nil || before != after {
		return startupInspectionUnresolved, nil
	}
	reason := malformedSessionMetaReason
	if before.sizeBytes == 0 {
		reason = emptySkipReason
	}
	skipped := state.RolloutMigrationSkippedRollout{
		RolloutPath:       relativeRolloutPath(codexHome, path),
		RolloutSizeBytes:  before.sizeBytes,
		RolloutModifiedNs: before.modifiedAtNs,
		SkipReason:        reason,
	}
	if err := state.RecordRolloutMigrationSkip(ctx, runtime.StateDB(), legacyToPaginatedMigrationID, skipped); err != nil {
		return startupInspectionUnresolved, err
	}
	return startupInspectionSkipped, nil
}

func recordSkipIfMalformed(ctx context.Context, runtime *state.StateRuntime, codexHome string, outcome *rollout.MigrationOutcome) error {
	fingerprint, err := rolloutFingerprint(outcome.RolloutPath)
	if err != nil {
		return nil
	}
	if _, metaErr := rollout.FirstSessionMeta(outcome.RolloutPath); metaErr == nil {
		return nil
	}
	reason := malformedSessionMetaReason
	if fingerprint.sizeBytes == 0 {
		reason = emptySkipReason
	}
	return state.RecordRolloutMigrationSkip(ctx, runtime.StateDB(), legacyToPaginatedMigrationID, state.RolloutMigrationSkippedRollout{
		RolloutPath:       relativeRolloutPath(codexHome, outcome.RolloutPath),
		RolloutSizeBytes:  fingerprint.sizeBytes,
		RolloutModifiedNs: fingerprint.modifiedAtNs,
		SkipReason:        reason,
	})
}

func revalidateSkippedRollouts(codexHome string, skipped []state.RolloutMigrationSkippedRollout) (map[string]bool, bool) {
	unchanged := map[string]bool{}
	invalidated := false
	for _, entry := range skipped {
		path := filepath.Join(codexHome, filepath.FromSlash(entry.RolloutPath))
		fingerprint, err := rolloutFingerprint(path)
		if err != nil {
			invalidated = true
			continue
		}
		if fingerprint.sizeBytes == entry.RolloutSizeBytes && fingerprint.modifiedAtNs == entry.RolloutModifiedNs {
			unchanged[entry.RolloutPath] = true
		} else {
			invalidated = true
		}
	}
	return unchanged, invalidated
}

type rolloutFileFingerprint struct {
	sizeBytes    int64
	modifiedAtNs int64
}

func rolloutFingerprint(path string) (rolloutFileFingerprint, error) {
	info, err := os.Stat(path)
	if err != nil {
		return rolloutFileFingerprint{}, err
	}
	modifiedNs := int64(0)
	if modified := info.ModTime(); !modified.IsZero() {
		modifiedNs = modified.UnixNano()
	}
	return rolloutFileFingerprint{sizeBytes: info.Size(), modifiedAtNs: modifiedNs}, nil
}

func advanceLastCheckedThread(ctx context.Context, runtime *state.StateRuntime, paths []string) error {
	var last *state.RolloutMigrationCursor
	for _, path := range paths {
		cursor := threadCreationCursor(path)
		if cursor == nil {
			continue
		}
		if last == nil || cursor.ThreadCreatedAt > last.ThreadCreatedAt || (cursor.ThreadCreatedAt == last.ThreadCreatedAt && cursor.ThreadID > last.ThreadID) {
			copy := *cursor
			last = &copy
		}
	}
	return state.AdvanceRolloutMigrationState(ctx, runtime.StateDB(), legacyToPaginatedMigrationID, last)
}

func findAllRolloutPaths(codexHome string) ([]string, error) {
	var paths []string
	for _, root := range []string{
		filepath.Join(codexHome, rollout.SessionsSubdir),
		filepath.Join(codexHome, rollout.ArchivedSessionsSubdir),
	} {
		collected, err := rollout.CollectRolloutPaths(root)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
			continue
		}
		paths = append(paths, collected...)
	}
	sort.Strings(paths)
	return paths, nil
}

func relativeRolloutPath(codexHome string, path string) string {
	rel, err := filepath.Rel(codexHome, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// threadCreationCursor mirrors Rust thread_creation_cursor: the rollout file
// name embeds a creation timestamp and the 36-char thread id.
func threadCreationCursor(path string) *state.RolloutMigrationCursor {
	name := filepath.Base(path)
	stem := strings.TrimSuffix(name, ".jsonl.zst")
	stem = strings.TrimSuffix(stem, ".jsonl")
	if !strings.HasPrefix(stem, "rollout-") {
		return nil
	}
	stem = strings.TrimPrefix(stem, "rollout-")
	const idLen = 36
	if len(stem) < idLen+1 {
		return nil
	}
	// Mirrors Rust thread_creation_cursor: separator = len - 37 so the
	// timestamp is stem[..separator] ("%Y-%m-%dT%H-%M-%S") and the thread id
	// is the final 36-char UUID after the "-" separator.
	separator := len(stem) - idLen - 1
	if separator < 0 {
		return nil
	}
	threadID := stem[separator+1:]
	timestampText := stem[:separator]
	created, err := time.Parse("2006-01-02T15-04-05", timestampText)
	if err != nil {
		return nil
	}
	return &state.RolloutMigrationCursor{ThreadCreatedAt: created.UTC().Unix(), ThreadID: threadID}
}

func hasPendingMigrationThreads(codexHome string) bool {
	dir := filepath.Join(codexHome, rollout.MigrationJournalDirectory)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".pending") {
			return true
		}
	}
	return false
}
