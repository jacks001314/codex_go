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
)

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
		return err
	}
	if guard == nil {
		// Rollout maintenance is already running for this Codex home.
		return nil
	}
	defer guard.Release()

	marker, err := tryClaimCompressionRunMarker(codexHome)
	if err != nil {
		return err
	}
	if marker == nil {
		// A recent pass already ran or is still running.
		return nil
	}
	defer marker.release()

	startedAt := time.Now()
	stats := RolloutCompressionStats{}
	deadline := startedAt.Add(compressionWorkerMaxRuntime)
	for _, root := range []string{
		filepath.Join(codexHome, ArchivedSessionsSubdir),
		filepath.Join(codexHome, SessionsSubdir),
	} {
		if !time.Now().Before(deadline) {
			break
		}
		if err := compressRolloutsInRoot(ctx, root, deadline, &stats); err != nil {
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
	// Persist the marker so a finished pass is not cleaned up: the stale window
	// is what prevents overlapping or too-frequent runs.
	marker.persist()
	return nil
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
func compressRolloutsInRoot(ctx context.Context, root string, deadline time.Time, stats *RolloutCompressionStats) error {
	paths, err := CollectRolloutPaths(root)
	if err != nil {
		return err
	}
	sem := make(chan struct{}, maxConcurrentCompressions)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, path := range paths {
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			break
		}
		if strings.HasSuffix(strings.ToLower(path), ".zst") {
			continue
		}
		meta, err := FirstSessionMeta(path)
		if err != nil {
			mu.Lock()
			stats.Skipped++
			mu.Unlock()
			continue
		}
		threadID := strings.TrimSpace(meta.ID)
		if threadID == "" {
			mu.Lock()
			stats.Skipped++
			mu.Unlock()
			continue
		}
		mu.Lock()
		stats.Scanned++
		mu.Unlock()
		sem <- struct{}{}
		wg.Add(1)
		go func(path string, threadID string) {
			defer wg.Done()
			defer func() { <-sem }()
			outcome, err := compressRolloutIfCold(path, threadID)
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
func compressRolloutIfCold(path string, threadID string) (compressionOutcome, error) {
	before, err := coldRolloutState(path)
	if err != nil {
		return "", err
	}
	if before == nil {
		return compressionSkippedNotCold, nil
	}
	compressedPath := path + ".zst"
	if _, err := os.Stat(compressedPath); err == nil {
		return compressionSkippedAlreadyCompressed, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	_ = threadID
	temp, err := os.CreateTemp(filepath.Dir(path), "rollout-compress-*.tmp")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := encodeRolloutZstd(path, temp); err != nil {
		temp.Close()
		return "", err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := verifyZstdRollout(tempPath); err != nil {
		return "", err
	}
	same, err := sameRolloutState(path, before)
	if err != nil {
		return "", err
	}
	if !same {
		return compressionSkippedChanged, nil
	}
	// Keep the source's timestamp so cold-file decisions stay stable across the
	// representation change.
	if err := os.Chtimes(tempPath, before.modTime, before.modTime); err != nil {
		return "", err
	}
	if _, err := os.Stat(compressedPath); err == nil {
		return compressionSkippedAlreadyCompressed, nil
	}
	if err := os.Rename(tempPath, compressedPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return compressionSkippedAlreadyCompressed, nil
		}
		return "", err
	}
	if same, err := sameRolloutState(path, before); err != nil || !same {
		_ = os.Remove(compressedPath)
		if err != nil {
			return "", err
		}
		return compressionSkippedChanged, nil
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return compressionCompressed, nil
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
