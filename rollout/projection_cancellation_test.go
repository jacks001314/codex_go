package rollout

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// countingCancellation mirrors the Rust fixture's `Cell` counter: the predicate
// reports a cancelled caller from the Nth check on.
func countingCancellation(limit int) func() bool {
	checks := 0
	return func() bool {
		checks++
		return checks >= limit
	}
}

func writeCancellationRollout(t *testing.T, compressed bool, records int) string {
	t.Helper()
	dir := t.TempDir()
	plain := filepath.Join(dir, "rollout-2026-07-21T10-00-00-thread.jsonl")
	contents := ""
	for ordinal := 0; ordinal < records; ordinal++ {
		contents += projectionLineJSON(uint64(ordinal), "turn-"+string(rune('a'+ordinal)))
	}
	if !compressed {
		if err := os.WriteFile(plain, []byte(contents), 0o640); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", plain, err)
		}
		return plain
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	if err := os.WriteFile(plain+".zst", encoder.EncodeAll([]byte(contents), nil), 0o640); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", plain+".zst", err)
	}
	return plain
}

// TestReadProjectionStepsStopsWhenCancelledLikeRust mirrors Rust #53947: a
// cancelled caller fails the rollout read instead of returning the history that
// was scanned so far, and the failure is never EOF.
func TestReadProjectionStepsStopsWhenCancelledLikeRust(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		name := "plain"
		if compressed {
			name = "zstd"
		}
		t.Run(name, func(t *testing.T) {
			path := writeCancellationRollout(t, compressed, 4)

			// Control: the same read completes when the caller stays alive.
			steps, nextOffset, err := ReadProjectionStepsCancellable(path, 0, 0, nil, func() bool { return false })
			if err != nil || len(steps) != 4 {
				t.Fatalf("uncancelled read = steps %d offset %d err %v, want 4 records", len(steps), nextOffset, err)
			}

			steps, _, err = ReadProjectionStepsCancellable(path, 0, 0, nil, countingCancellation(3))
			if !errors.Is(err, ErrRolloutReadCancelled) {
				t.Fatalf("cancelled read error = %v, want %v", err, ErrRolloutReadCancelled)
			}
			if steps != nil {
				t.Fatalf("cancelled read steps = %#v, want none", steps)
			}
		})
	}
}

// TestReadProjectionStepsChecksCancellationBetweenRecordsLikeRust pins the
// record-level check Rust added ("between history records"): the records before
// the cancelled check are not returned either, because the whole read fails.
func TestReadProjectionStepsChecksCancellationBetweenRecordsLikeRust(t *testing.T) {
	contents := []byte("")
	for ordinal := 0; ordinal < 4; ordinal++ {
		contents = append(contents, []byte(projectionLineJSON(uint64(ordinal), "turn-a"))...)
	}

	steps, _, err := readProjectionStepsData(contents, 0, 0, nil, nil)
	if err != nil || len(steps) != 4 {
		t.Fatalf("uncancelled read = steps %d err %v, want 4 records", len(steps), err)
	}

	// The third record's check observes a cancelled caller.
	steps, _, err = readProjectionStepsData(contents, 0, 0, nil, countingCancellation(3))
	if !errors.Is(err, ErrRolloutReadCancelled) {
		t.Fatalf("between-records cancellation error = %v, want %v", err, ErrRolloutReadCancelled)
	}
	if steps != nil {
		t.Fatalf("between-records cancellation steps = %#v, want none", steps)
	}
}

// TestReadProjectionStepsCancellationPrecedesTheFileLikeRust pins the entry
// check: an already cancelled caller never falls through to os.ErrNotExist or
// to an empty success on a missing rollout.
func TestReadProjectionStepsCancellationPrecedesTheFileLikeRust(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "rollout-missing.jsonl")

	steps, nextOffset, err := ReadProjectionStepsCancellable(missing, 0, 0, nil, func() bool { return true })
	if !errors.Is(err, ErrRolloutReadCancelled) {
		t.Fatalf("cancelled missing rollout error = %v, want %v", err, ErrRolloutReadCancelled)
	}
	if steps != nil || nextOffset != 0 {
		t.Fatalf("cancelled missing rollout = steps %#v offset %d, want none", steps, nextOffset)
	}

	// Control: without cancellation a missing rollout is still an empty prefix.
	steps, nextOffset, err = ReadProjectionStepsCancellable(missing, 0, 0, nil, nil)
	if err != nil || steps != nil || nextOffset != 0 {
		t.Fatalf("missing rollout without cancellation = steps %#v offset %d err %v", steps, nextOffset, err)
	}
}
