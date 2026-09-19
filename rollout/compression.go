package rollout

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	"codex_go/metrics"
)

// tag mirrors Rust's `RolloutCompressionTrigger::tag`.
func (t RolloutCompressionTrigger) tag() string {
	return string(t)
}

// Constants mirror Rust's `codex_rollout::compression` worker.
const (
	rolloutCompressionTempSuffix = ".tmp"
	minRolloutAge                = 7 * 24 * time.Hour
	runMarkerStaleAfter          = 6 * time.Hour
	compressionWorkerMaxRuntime  = 5 * time.Hour
	compressionRunMarkerFileName = "rollout-compression.lock"
	maxConcurrentCompressions    = 2
)

// RolloutCompressionTrigger records which entry point asked for a compression
// pass (Rust's `RolloutCompressionTrigger`).
type RolloutCompressionTrigger string

const (
	// RolloutCompressionTriggerStartup is a pass requested while starting the
	// local thread store.
	RolloutCompressionTriggerStartup RolloutCompressionTrigger = "startup"
	// RolloutCompressionTriggerRPC is a pass requested through `rollout/compress`.
	RolloutCompressionTriggerRPC RolloutCompressionTrigger = "rpc"
)

// RolloutCompressionStats mirrors Rust's worker counters.
type RolloutCompressionStats struct {
	Scanned    int
	Compressed int
	Skipped    int
	Failed     int
	// ScanErrors / CleanupErrors / TimeBudgetExhausted feed the completion
	// metrics (Rust's CompressionStats flags).
	ScanErrors          bool
	CleanupErrors       bool
	TimeBudgetExhausted bool
}

// SpawnRolloutCompressionWorker starts Rust's fire-and-forget background job
// that compresses cold local rollout files. Failures are logged; nothing blocks
// on the pass.
func SpawnRolloutCompressionWorker(codexHome string, trigger RolloutCompressionTrigger) {
	go func() {
		if err := RunRolloutCompression(context.Background(), codexHome, trigger); err != nil {
			slog.Warn("rollout compression worker failed", "codexHome", codexHome, "error", err)
		}
	}()
}

// RunRolloutCompression performs one bounded compression pass over the local
// rollouts under codexHome. It returns nil when the maintenance lock or the run
// marker shows another pass is already running.
func RunRolloutCompression(ctx context.Context, codexHome string, trigger RolloutCompressionTrigger) error {
	guard, err := TryAcquireRolloutMaintenanceLock(codexHome)
	if err != nil {
		rolloutCompressionFailure(rolloutCompressionRunCounter, "status", &trigger, "maintenance_lock", err)
		return err
	}
	if guard == nil {
		// Rollout maintenance is already running for this Codex home.
		rolloutCompressionRun(trigger, "skipped_maintenance")
		return nil
	}
	defer guard.Release()

	marker, err := tryClaimCompressionRunMarker(codexHome)
	if err != nil {
		rolloutCompressionFailure(rolloutCompressionRunCounter, "status", &trigger, "run_marker", err)
		return err
	}
	if marker == nil {
		// A recent pass already ran or is still running.
		rolloutCompressionRun(trigger, "skipped_already_running")
		return nil
	}
	defer marker.release()

	rolloutCompressionRun(trigger, "started")
	startedAt := time.Now()
	stats := RolloutCompressionStats{}
	// Rust cleans up temp files from interrupted passes before scanning, and a
	// cleanup error is reported without failing the pass.
	stats.CleanupErrors = cleanStaleRolloutTemps(codexHome, trigger)
	deadline := startedAt.Add(compressionWorkerMaxRuntime)
	for _, root := range []string{
		filepath.Join(codexHome, ArchivedSessionsSubdir),
		filepath.Join(codexHome, SessionsSubdir),
	} {
		if !time.Now().Before(deadline) {
			stats.TimeBudgetExhausted = true
			break
		}
		if err := compressRolloutsInRoot(ctx, root, deadline, &stats, trigger); err != nil {
			rolloutCompressionFailure(rolloutCompressionRunCounter, "status", &trigger, "scan", err)
			rolloutCompressionRunDurationTags(
				map[string]string{"status": "failed", "trigger": trigger.tag()},
				time.Since(startedAt),
			)
			return err
		}
	}
	slog.Info(
		"rollout compression worker finished",
		"trigger", string(trigger),
		"scanned", stats.Scanned,
		"compressed", stats.Compressed,
		"skipped", stats.Skipped,
		"failed", stats.Failed,
	)
	// Rust keeps the completed outcome even when a scan or cleanup failed: it
	// means the pass returned, not that every directory or file was handled.
	completion := "scan_finished"
	if stats.TimeBudgetExhausted {
		completion = "time_budget"
	}
	completionTags := rolloutCompressionRunTags(trigger, "completed", map[string]string{
		"completion_reason": completion,
		"file_errors":       rolloutCompressionBoolTag(stats.Failed > 0),
		"scan_errors":       rolloutCompressionBoolTag(stats.ScanErrors),
		"cleanup_errors":    rolloutCompressionBoolTag(stats.CleanupErrors),
	})
	metrics.Counter(rolloutCompressionRunCounter, 1, completionTags)
	metrics.RecordDuration(rolloutCompressionRunDurationMetric, time.Since(startedAt), completionTags)
	// Persist the marker so a finished pass is not cleaned up: the stale window
	// is what prevents overlapping or too-frequent runs.
	marker.persist()
	return nil
}

// cleanStaleRolloutTemps mirrors Rust's `cleanup_stale_temps`: temp files left
// behind by an interrupted pass are removed once they are older than the run
// marker's stale window. Scan and removal errors are reported and the pass
// continues.
func cleanStaleRolloutTemps(codexHome string, trigger RolloutCompressionTrigger) bool {
	errorsSeen := false
	// Rust sweeps the whole Codex home, not just the sessions roots.
	for _, root := range []string{codexHome} {
		if _, err := os.Stat(root); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			errorsSeen = true
			rolloutCompressionFailure(rolloutCompressionTempCleanupCounter, "outcome", &trigger, "check_root", err)
			continue
		}
		stack := []string{root}
		for len(stack) > 0 {
			dir := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			entries, err := os.ReadDir(dir)
			if err != nil {
				errorsSeen = true
				rolloutCompressionFailure(rolloutCompressionTempCleanupCounter, "outcome", &trigger, "read_directory", err)
				continue
			}
			for _, entry := range entries {
				path := filepath.Join(dir, entry.Name())
				if entry.IsDir() {
					stack = append(stack, path)
					continue
				}
				if !strings.HasSuffix(strings.ToLower(entry.Name()), rolloutCompressionTempSuffix) {
					continue
				}
				info, err := entry.Info()
				if err != nil {
					errorsSeen = true
					rolloutCompressionFailure(rolloutCompressionTempCleanupCounter, "outcome", &trigger, "read_metadata", err)
					continue
				}
				if time.Since(info.ModTime()) < runMarkerStaleAfter {
					continue
				}
				if err := os.Remove(path); err != nil {
					if errors.Is(err, os.ErrNotExist) {
						continue
					}
					errorsSeen = true
					rolloutCompressionFailure(rolloutCompressionTempCleanupCounter, "outcome", &trigger, "remove_temp", err)
					continue
				}
				rolloutCompressionTempCleanup(trigger, "removed")
			}
		}
	}
	return errorsSeen
}

// compressionRunMarker mirrors Rust's `CompressionRunMarker`.
type compressionRunMarker struct {
	path         string
	removeOnDrop bool
}

func (m *compressionRunMarker) persist() {
	if m == nil {
		return
	}
	m.removeOnDrop = false
}

func (m *compressionRunMarker) release() {
	if m == nil || !m.removeOnDrop {
		return
	}
	_ = os.Remove(m.path)
}

// tryClaimCompressionRunMarker mirrors Rust's `CompressionRunMarker::try_claim`.
func tryClaimCompressionRunMarker(codexHome string) (*compressionRunMarker, error) {
	markerDir := filepath.Join(codexHome, ".tmp")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(markerDir, compressionRunMarkerFileName)
	if err := createCompressionRunMarker(path); err == nil {
		return &compressionRunMarker{path: path, removeOnDrop: true}, nil
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if time.Since(info.ModTime()) < runMarkerStaleAfter {
		return nil, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := createCompressionRunMarker(path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, nil
		}
		return nil, err
	}
	return &compressionRunMarker{path: path, removeOnDrop: true}, nil
}

func createCompressionRunMarker(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(file, "pid=%d started_at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// compressionOutcome mirrors Rust's `CompressionOutcome`.
type compressionOutcome string

const (
	compressionCompressed               compressionOutcome = "compressed"
	compressionSkippedNotCold           compressionOutcome = "skipped_not_cold"
	compressionSkippedBusy              compressionOutcome = "skipped_busy"
	compressionSkippedChanged           compressionOutcome = "skipped_changed"
	compressionSkippedAlreadyCompressed compressionOutcome = "skipped_already_compressed"
)

// compressRolloutsInRoot mirrors Rust's `compress_rollouts_in_root`: walk a
// sessions root and compress every cold plain rollout.
func compressRolloutsInRoot(ctx context.Context, root string, deadline time.Time, stats *RolloutCompressionStats, trigger RolloutCompressionTrigger) error {
	// Rust tolerates a missing or unreadable root, reporting it as a scan error
	// instead of failing the pass.
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		stats.ScanErrors = true
		rolloutCompressionFailure(rolloutCompressionScanCounter, "outcome", &trigger, "check_root", err)
		return nil
	}
	paths, err := CollectRolloutPaths(root)
	if err != nil {
		stats.ScanErrors = true
		rolloutCompressionFailure(rolloutCompressionScanCounter, "outcome", &trigger, "read_directory", err)
		return nil
	}
	sem := make(chan struct{}, maxConcurrentCompressions)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, path := range paths {
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			if ctx.Err() == nil {
				mu.Lock()
				stats.TimeBudgetExhausted = true
				mu.Unlock()
			}
			break
		}
		if strings.HasSuffix(strings.ToLower(path), ".zst") {
			continue
		}
		meta, err := FirstSessionMeta(path)
		if err != nil {
			mu.Lock()
			stats.ScanErrors = true
			stats.Skipped++
			mu.Unlock()
			rolloutCompressionFailure(rolloutCompressionScanCounter, "outcome", &trigger, "read_metadata", err)
			rolloutCompressionFile(trigger, "skipped_unreadable_meta")
			continue
		}
		threadID := strings.TrimSpace(meta.ID)
		if threadID == "" {
			mu.Lock()
			stats.ScanErrors = true
			stats.Skipped++
			mu.Unlock()
			rolloutCompressionFile(trigger, "skipped_unreadable_meta")
			continue
		}
		mu.Lock()
		stats.Scanned++
		mu.Unlock()
		rolloutCompressionFile(trigger, "scanned")
		sem <- struct{}{}
		wg.Add(1)
		go func(path string, threadID string) {
			defer wg.Done()
			defer func() { <-sem }()
			outcome, err := compressRolloutIfCold(path, threadID, trigger)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				stats.Failed++
				slog.Warn("failed to compress rollout", "path", path, "error", err)
				return
			}
			if outcome == compressionCompressed {
				stats.Compressed++
				return
			}
			stats.Skipped++
		}(path, threadID)
	}
	wg.Wait()
	return nil
}

// compressRolloutIfCold mirrors Rust's `compress_rollout_if_cold_blocking`: a
// cold plain rollout is encoded to a sibling temp file, verified, and published
// with a no-clobber rename before the plain file is removed.
func compressRolloutIfCold(path string, threadID string, trigger RolloutCompressionTrigger) (compressionOutcome, error) {
	startedAt := time.Now()
	outcome, sourceBytes, compressedBytes, err := compressRolloutIfColdInner(path, threadID)
	if err != nil {
		// Rust labels the failing stage; Go's encode/publish steps are one
		// function, so the pass records the single "compress" stage.
		rolloutCompressionFailure(rolloutCompressionFileCounter, "outcome", &trigger, "compress", err)
		rolloutCompressionFileDuration(trigger, "failed", time.Since(startedAt))
		return "", err
	}
	rolloutCompressionFile(trigger, string(outcome))
	rolloutCompressionFileDuration(trigger, string(outcome), time.Since(startedAt))
	if outcome == compressionCompressed {
		rolloutCompressionSourceBytes(trigger, string(outcome), sourceBytes)
		rolloutCompressionCompressedBytes(trigger, string(outcome), compressedBytes)
		rolloutCompressionRatio(trigger, string(outcome), sourceBytes, compressedBytes)
	}
	return outcome, nil
}

func compressRolloutIfColdInner(path string, threadID string) (compressionOutcome, int64, int64, error) {
	before, err := coldRolloutState(path)
	if err != nil {
		return "", 0, 0, err
	}
	if before == nil {
		return compressionSkippedNotCold, 0, 0, nil
	}
	compressedPath := path + ".zst"
	if _, err := os.Stat(compressedPath); err == nil {
		return compressionSkippedAlreadyCompressed, before.size, 0, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", 0, 0, err
	}
	_ = threadID
	temp, err := os.CreateTemp(filepath.Dir(path), "rollout-compress-*.tmp")
	if err != nil {
		return "", 0, 0, err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := encodeRolloutZstd(path, temp); err != nil {
		temp.Close()
		return "", 0, 0, err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return "", 0, 0, err
	}
	if err := temp.Close(); err != nil {
		return "", 0, 0, err
	}
	if err := verifyZstdRollout(tempPath); err != nil {
		return "", 0, 0, err
	}
	same, err := sameRolloutState(path, before)
	if err != nil {
		return "", 0, 0, err
	}
	if !same {
		return compressionSkippedChanged, before.size, 0, nil
	}
	// Keep the source's timestamp so cold-file decisions stay stable across the
	// representation change.
	if err := os.Chtimes(tempPath, before.modTime, before.modTime); err != nil {
		return "", 0, 0, err
	}
	if _, err := os.Stat(compressedPath); err == nil {
		return compressionSkippedAlreadyCompressed, before.size, 0, nil
	}
	if err := os.Rename(tempPath, compressedPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return compressionSkippedAlreadyCompressed, before.size, 0, nil
		}
		return "", 0, 0, err
	}
	if same, err := sameRolloutState(path, before); err != nil || !same {
		_ = os.Remove(compressedPath)
		if err != nil {
			return "", 0, 0, err
		}
		return compressionSkippedChanged, before.size, 0, nil
	}
	if err := os.Remove(path); err != nil {
		return "", 0, 0, err
	}
	compressedBytes := int64(0)
	if info, err := os.Stat(compressedPath); err == nil {
		compressedBytes = info.Size()
	}
	return compressionCompressed, before.size, compressedBytes, nil
}

type rolloutFileState struct {
	size    int64
	modTime time.Time
}

// coldRolloutState returns the file state when the rollout is cold, nil when it
// is too fresh (or gone).
func coldRolloutState(path string) (*rolloutFileState, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	if time.Since(info.ModTime()) < minRolloutAge {
		return nil, nil
	}
	return &rolloutFileState{size: info.Size(), modTime: info.ModTime()}, nil
}

// sameRolloutState mirrors Rust's `same_file_state`: the source must not have
// changed while the replacement was prepared.
func sameRolloutState(path string, expected *rolloutFileState) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return info.Size() == expected.size && info.ModTime().Equal(expected.modTime), nil
}

// encodeRolloutZstd writes the zstd representation of a rollout file.
func encodeRolloutZstd(path string, writer io.Writer) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer source.Close()
	encoder, err := zstd.NewWriter(writer)
	if err != nil {
		return err
	}
	if _, err := io.Copy(encoder, source); err != nil {
		encoder.Close()
		return err
	}
	return encoder.Close()
}

// verifyZstdRollout mirrors Rust's `verify_zstd`: the published file must decode
// back to a complete stream.
func verifyZstdRollout(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder, err := zstd.NewReader(file)
	if err != nil {
		return err
	}
	defer decoder.Close()
	if _, err := io.Copy(io.Discard, decoder); err != nil {
		return err
	}
	return nil
}
