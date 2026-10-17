package rollout

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// Mirrors Rust
// `codex-rs/rollout/src/rollout_reference_index_tests.rs::unarchived_scan_finds_all_active_rollouts_owned_by_a_thread`
// (#41908): archiving only needs the active rollouts that still have to move,
// so the scan covers compressed and uncompressed active files owned by the
// thread and excludes archived or unrelated history.
func TestUnarchivedScanFindsAllActiveRolloutsOwnedByThreadLikeRust(t *testing.T) {
	home := t.TempDir()
	ownerID := "0192f0aa-0000-7000-8000-000000000070"
	originalID := "0192f0aa-0000-7000-8000-000000000071"
	replacementID := "0192f0aa-0000-7000-8000-000000000072"
	otherID := "0192f0aa-0000-7000-8000-000000000073"
	stamp := "2025-01-03T12-00-00"

	originalPath := filepath.Join(home, SessionsSubdir, "2025", "01", "03", fmt.Sprintf("rollout-%s-%s_%s.jsonl", stamp, ownerID, originalID))
	replacementPath := filepath.Join(home, SessionsSubdir, "2025", "01", "03", fmt.Sprintf("rollout-%s-%s_%s.jsonl", stamp, ownerID, replacementID))
	writeRolloutAt(t, home, originalPath, ownerID, nil)
	compressRolloutFile(t, originalPath)
	writeRolloutAt(t, home, replacementPath, ownerID, nil)
	// Archived history is never part of the active scan.
	writeRolloutAt(t, home, filepath.Join(home, ArchivedSessionsSubdir, fmt.Sprintf("rollout-%s-%s.jsonl", stamp, ownerID)), ownerID, nil)
	// Unrelated active rollouts are filtered out by their canonical filename.
	writeRolloutAt(t, home, filepath.Join(home, SessionsSubdir, "2025", "01", "03", fmt.Sprintf("rollout-%s-%s.jsonl", stamp, otherID)), otherID, nil)

	owned, err := OwnedUnarchivedThreadRollouts(home, []string{ownerID})
	if err != nil {
		t.Fatalf("OwnedUnarchivedThreadRollouts() error = %v", err)
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
		{rolloutID: originalID, path: originalPath + ".zst"},
		{rolloutID: replacementID, path: replacementPath},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("owned unarchived rollouts = %#v, want %#v", got, want)
	}
}

// Mirrors Rust `archive_thread_with_paths`
// (codex-rs/thread-store/src/local/archive_thread.rs, #41908): archiving a
// thread moves every active rollout it owns, reports the selected rollout's
// archived path, and never touches archived or unrelated files.
func TestArchiveThreadRolloutsMovesEveryOwnedActiveRolloutLikeRust(t *testing.T) {
	home := t.TempDir()
	ownerID := "0192f0aa-0000-7000-8000-000000000080"
	originalID := "0192f0aa-0000-7000-8000-000000000081"
	replacementID := "0192f0aa-0000-7000-8000-000000000082"
	otherID := "0192f0aa-0000-7000-8000-000000000083"
	stamp := "2025-01-03T12-00-00"
	sessionsDir := filepath.Join(home, SessionsSubdir, "2025", "01", "03")

	originalPath := filepath.Join(sessionsDir, fmt.Sprintf("rollout-%s-%s_%s.jsonl", stamp, ownerID, originalID))
	replacementPath := filepath.Join(sessionsDir, fmt.Sprintf("rollout-%s-%s_%s.jsonl", stamp, ownerID, replacementID))
	writeRolloutAt(t, home, originalPath, ownerID, nil)
	compressRolloutFile(t, originalPath)
	writeRolloutAt(t, home, replacementPath, ownerID, nil)
	otherPath := filepath.Join(sessionsDir, fmt.Sprintf("rollout-%s-%s.jsonl", stamp, otherID))
	writeRolloutAt(t, home, otherPath, otherID, nil)
	archivedOtherPath := filepath.Join(home, ArchivedSessionsSubdir, fmt.Sprintf("rollout-%s-%s.jsonl", stamp, ownerID))
	writeRolloutAt(t, home, archivedOtherPath, ownerID, nil)

	archivedPath, err := ArchiveThreadRollouts(home, []string{ownerID}, replacementPath)
	if err != nil {
		t.Fatalf("ArchiveThreadRollouts() error = %v", err)
	}
	archiveDir := filepath.Join(home, ArchivedSessionsSubdir)
	if want := filepath.Join(archiveDir, filepath.Base(replacementPath)); archivedPath != want {
		t.Fatalf("archived selected rollout = %q, want %q", archivedPath, want)
	}
	for _, name := range []string{filepath.Base(originalPath) + ".zst", filepath.Base(replacementPath)} {
		if _, err := os.Stat(filepath.Join(archiveDir, name)); err != nil {
			t.Fatalf("archived rollout %q missing: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(sessionsDir, name)); err == nil {
			t.Fatalf("rollout %q is still active after archiving", name)
		}
	}
	if _, err := os.Stat(otherPath); err != nil {
		t.Fatalf("unrelated active rollout must stay active: %v", err)
	}
	if _, err := os.Stat(archivedOtherPath); err != nil {
		t.Fatalf("already archived rollout must stay archived: %v", err)
	}
}
