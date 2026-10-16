package rollout

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Mirrors Rust
// `codex-rs/rollout/src/rollout_reference_index_tests.rs::filtered_archived_scan_includes_only_owned_archived_candidates`
// (#54060): restoring a thread must include its compressed/reverted files
// without scanning other history.
func TestFilteredArchivedScanIncludesOnlyOwnedArchivedCandidatesLikeRust(t *testing.T) {
	home := t.TempDir()
	ownerID := "0192f0aa-0000-7000-8000-00000000003c"
	replacementID := "0192f0aa-0000-7000-8000-00000000003d"
	otherID := "0192f0aa-0000-7000-8000-00000000003e"
	mismatchedID := "0192f0aa-0000-7000-8000-00000000003f"
	stamp := "2025-01-03T12-00-00"

	originalPath := filepath.Join(home, ArchivedSessionsSubdir, fmt.Sprintf("rollout-%s-%s.jsonl", stamp, ownerID))
	replacementPath := filepath.Join(home, ArchivedSessionsSubdir, fmt.Sprintf("rollout-%s-%s_%s.jsonl", stamp, ownerID, replacementID))
	writeRolloutAt(t, home, originalPath, ownerID, nil)
	compressRolloutFile(t, originalPath)
	writeRolloutAt(t, home, replacementPath, ownerID, &HistoryPosition{ThreadID: ownerID})

	// A matching filename is only a candidate: metadata must confirm ownership.
	mismatchedPath := filepath.Join(home, ArchivedSessionsSubdir, fmt.Sprintf("rollout-%s-%s_%s.jsonl", stamp, ownerID, mismatchedID))
	writeRolloutAt(t, home, mismatchedPath, otherID, nil)
	// Unrelated filenames are excluded before reading their metadata.
	writeRolloutAt(t, home, filepath.Join(home, ArchivedSessionsSubdir, fmt.Sprintf("rollout-%s-%s.jsonl", stamp, otherID)), ownerID, nil)
	// Active rollouts are never part of the archived scan.
	writeRolloutAt(t, home, filepath.Join(home, SessionsSubdir, fmt.Sprintf("rollout-%s-%s.jsonl", stamp, ownerID)), ownerID, nil)

	owned, err := OwnedArchivedThreadRollouts(home, []string{ownerID})
	if err != nil {
		t.Fatalf("OwnedArchivedThreadRollouts() error = %v", err)
	}
	type entry struct {
		rolloutID string
		path      string
	}
	got := make([]entry, 0, len(owned))
	for _, path := range owned {
		parsed, ok := ParseRolloutFileName(filepath.Base(path))
		if !ok {
			t.Fatalf("owned path %q is not a canonical rollout filename", path)
		}
		got = append(got, entry{rolloutID: parsed.RolloutID, path: path})
	}
	sort.Slice(got, func(i, j int) bool { return got[i].rolloutID < got[j].rolloutID })
	want := []entry{
		{rolloutID: ownerID, path: originalPath + ".zst"},
		{rolloutID: replacementID, path: replacementPath},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("owned archived rollouts = %#v, want %#v", got, want)
	}
	if _, err := os.Stat(mismatchedPath); err != nil {
		t.Fatalf("mismatched ownership candidate must stay archived: %v", err)
	}
}

// Mirrors Rust `find_thread_path_by_id_str` (codex-rs/rollout/src/list.rs:1501):
// the newest archived rollout owned by the thread wins, so a reverted
// replacement beats the older rollout it superseded.
func TestFindThreadPathPrefersNewestArchivedRolloutLikeRust(t *testing.T) {
	home := t.TempDir()
	ownerID := "0192f0aa-0000-7000-8000-00000000004a"
	older := filepath.Join(home, ArchivedSessionsSubdir, fmt.Sprintf("rollout-2025-01-03T12-00-00-%s.jsonl", ownerID))
	newer := filepath.Join(home, ArchivedSessionsSubdir, fmt.Sprintf("rollout-2025-01-04T12-00-00-%s.jsonl", ownerID))
	writeRolloutAt(t, home, older, ownerID, nil)
	writeRolloutAt(t, home, newer, ownerID, nil)

	found, err := FindThreadPath(home, ownerID, true)
	if err != nil {
		t.Fatalf("FindThreadPath() error = %v", err)
	}
	if found != newer {
		t.Fatalf("FindThreadPath() = %q, want the newest archived rollout %q", found, newer)
	}
}

// Rust keeps the rollout candidate set to canonical filenames
// (`visit_rollout_filenames`, list.rs:1444), so a renamed file is never opened
// and never wins over the thread's real rollout (Rust #54060).
func TestFindThreadPathIgnoresNonCanonicalArchivedRolloutLikeRust(t *testing.T) {
	home := t.TempDir()
	ownerID := "0192f0aa-0000-7000-8000-000000000050"
	archivedDir := filepath.Join(home, ArchivedSessionsSubdir)
	stray := filepath.Join(archivedDir, "not-a-rollout.jsonl")
	writeRolloutAt(t, home, stray, ownerID, nil)

	if found, err := FindThreadPath(home, ownerID, true); err == nil {
		t.Fatalf("FindThreadPath() = %q, want no match for a non-canonical archived filename", found)
	}

	canonical := filepath.Join(archivedDir, fmt.Sprintf("rollout-2025-01-03T12-00-00-%s.jsonl", ownerID))
	writeRolloutAt(t, home, canonical, ownerID, nil)
	found, err := FindThreadPath(home, ownerID, true)
	if err != nil {
		t.Fatalf("FindThreadPath() error = %v", err)
	}
	if found != canonical {
		t.Fatalf("FindThreadPath() = %q, want the canonical rollout %q", found, canonical)
	}
}

// Mirrors Rust `unarchive_thread` (thread-store/src/local/unarchive_thread.rs):
// the selected rollout and every other archived rollout owned by the thread are
// restored, including a compressed original superseded by a reverted
// replacement.
func TestRestoreThreadRolloutsMovesEveryOwnedArchivedRolloutLikeRust(t *testing.T) {
	home := t.TempDir()
	ownerID := "0192f0aa-0000-7000-8000-000000000060"
	replacementID := "0192f0aa-0000-7000-8000-000000000061"
	stamp := "2025-01-03T12-00-00"
	originalPath := filepath.Join(home, ArchivedSessionsSubdir, fmt.Sprintf("rollout-%s-%s.jsonl", stamp, ownerID))
	replacementPath := filepath.Join(home, ArchivedSessionsSubdir, fmt.Sprintf("rollout-%s-%s_%s.jsonl", stamp, ownerID, replacementID))
	writeRolloutAt(t, home, originalPath, ownerID, nil)
	compressRolloutFile(t, originalPath)
	writeRolloutAt(t, home, replacementPath, ownerID, &HistoryPosition{ThreadID: ownerID})

	selected, err := FindThreadPath(home, ownerID, true)
	if err != nil {
		t.Fatalf("FindThreadPath() error = %v", err)
	}
	if selected != replacementPath {
		t.Fatalf("selected archived rollout = %q, want the reverted replacement %q", selected, replacementPath)
	}
	restored, err := RestoreThreadRollouts(home, ownerID, selected)
	if err != nil {
		t.Fatalf("RestoreThreadRollouts() error = %v", err)
	}
	sessionsDir := filepath.Join(home, SessionsSubdir, "2025", "01", "03")
	if want := filepath.Join(sessionsDir, filepath.Base(replacementPath)); restored != want {
		t.Fatalf("restored path = %q, want %q", restored, want)
	}
	for _, name := range []string{filepath.Base(originalPath) + ".zst", filepath.Base(replacementPath)} {
		if _, err := os.Stat(filepath.Join(sessionsDir, name)); err != nil {
			t.Fatalf("restored rollout %q missing: %v", name, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(home, ArchivedSessionsSubdir))
	if err != nil {
		t.Fatalf("ReadDir(archived) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("archived directory still holds %d entries", len(entries))
	}
}

// writeRolloutAt pins a rollout at an exact path so tests can build non-canonical
// or reverted filenames.
func writeRolloutAt(t *testing.T, home string, path string, threadID string, historyBase *HistoryPosition) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(path), err)
	}
	recorder, err := NewRecorder(&CreateParams{
		CodexHome:     home,
		ThreadID:      threadID,
		Source:        "cli",
		CWD:           "/repo",
		ModelProvider: "openai",
		HistoryMode:   "legacy",
		HistoryBase:   historyBase,
		Now:           time.Date(2025, 1, 3, 12, 0, 0, 0, time.UTC),
		Path:          path,
	})
	if err != nil {
		t.Fatalf("NewRecorder(%s) error = %v", path, err)
	}
	if err := recorder.AppendItem(Item{Type: "user_message", Data: map[string]any{"text": "archived"}}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// compressRolloutFile replaces a plain rollout with its compressed sibling,
// mirroring Rust's `compress_now`.
func compressRolloutFile(t *testing.T, plain string) {
	t.Helper()
	data, err := os.ReadFile(plain)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", plain, err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("zstd.NewWriter() error = %v", err)
	}
	defer encoder.Close()
	if err := os.WriteFile(plain+".zst", encoder.EncodeAll(data, nil), 0o600); err != nil {
		t.Fatalf("WriteFile(%s.zst) error = %v", plain, err)
	}
	if err := os.Remove(plain); err != nil {
		t.Fatalf("Remove(%s) error = %v", plain, err)
	}
}
