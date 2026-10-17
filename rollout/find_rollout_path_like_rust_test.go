package rollout

import (
	"fmt"
	"path/filepath"
	"testing"
)

// Mirrors Rust `find_rollout_path_by_rollout_id`
// (codex-rs/rollout/src/list.rs, #47653): the lookup is by immutable rollout id,
// not by thread id, and it searches the active tree before the archive.
func TestFindRolloutPathByRolloutIDLikeRust(t *testing.T) {
	home := t.TempDir()
	threadID := "0192f0aa-0000-7000-8000-0000000000f0"
	rolloutID := "0192f0aa-0000-7000-8000-0000000000f1"
	stamp := "2026-09-22T12-00-00"

	active := filepath.Join(home, SessionsSubdir, "2026", "09", "22", fmt.Sprintf("rollout-%s-%s_%s.jsonl", stamp, threadID, rolloutID))
	writeRolloutAt(t, home, active, threadID, nil)
	found, ok, err := FindRolloutPathByRolloutID(home, rolloutID)
	if err != nil || !ok {
		t.Fatalf("FindRolloutPathByRolloutID(active): ok=%v err=%v", ok, err)
	}
	if found != active {
		t.Fatalf("found = %q, want %q", found, active)
	}

	// The thread id must not resolve through the rollout-id entry point.
	if found, ok, err := FindRolloutPathByRolloutID(home, "missing-rollout"); err != nil || ok {
		t.Fatalf("missing rollout: found=%q ok=%v err=%v", found, ok, err)
	}

	// Archived rollouts are searched when the active tree has no match.
	archivedThread := "0192f0aa-0000-7000-8000-0000000000f2"
	archivedRollout := "0192f0aa-0000-7000-8000-0000000000f3"
	archived := filepath.Join(home, ArchivedSessionsSubdir, fmt.Sprintf("rollout-%s-%s_%s.jsonl", stamp, archivedThread, archivedRollout))
	writeRolloutAt(t, home, archived, archivedThread, nil)
	found, ok, err = FindRolloutPathByRolloutID(home, archivedRollout)
	if err != nil || !ok {
		t.Fatalf("FindRolloutPathByRolloutID(archived): ok=%v err=%v", ok, err)
	}
	if found != archived {
		t.Fatalf("found = %q, want %q", found, archived)
	}

	// A plain sibling hides its compressed copy, mirroring RolloutFile::from_path.
	plainThread := "0192f0aa-0000-7000-8000-0000000000f4"
	plainRollout := "0192f0aa-0000-7000-8000-0000000000f5"
	plain := filepath.Join(home, SessionsSubdir, "2026", "09", "22", fmt.Sprintf("rollout-%s-%s_%s.jsonl", stamp, plainThread, plainRollout))
	writeRolloutAt(t, home, plain, plainThread, nil)
	compressRolloutFile(t, plain)
	writeRolloutAt(t, home, plain, plainThread, nil)
	found, ok, err = FindRolloutPathByRolloutID(home, plainRollout)
	if err != nil || !ok {
		t.Fatalf("FindRolloutPathByRolloutID(plain sibling): ok=%v err=%v", ok, err)
	}
	if found != plain {
		t.Fatalf("found = %q, want the plain sibling %q", found, plain)
	}
}
