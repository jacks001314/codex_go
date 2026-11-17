package session

import "testing"

// TestCheckpointSamplingAnnotatesReplacementAgainstLivePrefixLikeRust mirrors
// Rust `SamplingProgress::checkpoint`
// (`codex-rs/core/src/context_manager/sampling.rs`), called by
// `Session::checkpoint_sampling` before a compaction replacement is installed.
//
// The replacement is annotated against the history it replaces: new items are
// recorded after the live history's last sequence, and the first replacement
// item acknowledges the live sampling prefix. The replacement's own items are
// never acknowledged, so replay cannot treat rebuilt input as delivered.
func TestCheckpointSamplingAnnotatesReplacementAgainstLivePrefixLikeRust(t *testing.T) {
	live := []Item{{ID: "u1"}, {ID: "a1"}}
	MergeItemHarnessMetadata(&live[0], map[string]any{
		harnessHistorySequenceKey:        uint64(1),
		harnessSampledThroughSequenceKey: uint64(1),
	})
	MergeItemHarnessMetadata(&live[1], map[string]any{
		harnessHistorySequenceKey:        uint64(2),
		harnessSampledThroughSequenceKey: uint64(2),
	})
	replacement := []Item{{ID: "summary"}, {ID: "first-user"}}

	last := CheckpointSampling(replacement, live)
	if last != 4 {
		t.Fatalf("CheckpointSampling() last = %d, want 4", last)
	}
	for index, want := range []uint64{3, 4} {
		if got := ItemHistorySequence(&replacement[index]); got != want {
			t.Fatalf("replacement[%d] history_sequence = %d, want %d", index, got, want)
		}
		if got := ItemSampledThroughSequence(&replacement[index]); got != 2 {
			t.Fatalf("replacement[%d] sampled_through = %d, want the live checkpoint 2", index, got)
		}
	}
	// The live history is untouched: checkpointing annotates only the replacement.
	if got := ItemHistorySequence(&live[1]); got != 2 {
		t.Fatalf("live history_sequence = %d, want 2", got)
	}
}

// TestCheckpointSamplingKeepsRetainedIdentitiesAndRaisesFirstCheckpointLikeRust
// covers a replacement that retains already-sequenced items: kept items keep
// their append identity, only new items are recorded, and the first item still
// acknowledges the highest live checkpoint.
func TestCheckpointSamplingKeepsRetainedIdentitiesAndRaisesFirstCheckpointLikeRust(t *testing.T) {
	live := []Item{{ID: "u1"}}
	MergeItemHarnessMetadata(&live[0], map[string]any{
		harnessHistorySequenceKey:        uint64(5),
		harnessSampledThroughSequenceKey: uint64(9),
	})
	retained := Item{ID: "kept"}
	MergeItemHarnessMetadata(&retained, map[string]any{
		harnessHistorySequenceKey: uint64(4),
	})
	replacement := []Item{{ID: "summary"}, retained}

	last := CheckpointSampling(replacement, live)
	if last != 10 {
		t.Fatalf("CheckpointSampling() last = %d, want 10", last)
	}
	if got := ItemHistorySequence(&replacement[0]); got != 10 {
		t.Fatalf("summary history_sequence = %d, want 10", got)
	}
	if got := ItemHistorySequence(&replacement[1]); got != 4 {
		t.Fatalf("retained history_sequence = %d, want the kept identity 4", got)
	}
	if got := ItemSampledThroughSequence(&replacement[0]); got != 9 {
		t.Fatalf("summary sampled_through = %d, want the live checkpoint 9", got)
	}
	if got := ItemSampledThroughSequence(&replacement[1]); got != 0 {
		t.Fatalf("retained sampled_through = %d, want absent", got)
	}
}

// TestCheckpointSamplingIgnoresEmptyReplacementLikeRust keeps the degenerate
// case honest: with nothing to install the live progress is left alone.
func TestCheckpointSamplingIgnoresEmptyReplacementLikeRust(t *testing.T) {
	live := []Item{{ID: "u1"}}
	MergeItemHarnessMetadata(&live[0], map[string]any{
		harnessHistorySequenceKey:        uint64(3),
		harnessSampledThroughSequenceKey: uint64(3),
	})
	if last := CheckpointSampling(nil, live); last != 3 {
		t.Fatalf("CheckpointSampling(nil) last = %d, want 3", last)
	}
}
