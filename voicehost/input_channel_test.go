package voicehost

import (
	"errors"
	"testing"
)

// TestInputChannelSelectionExcludesPlaybackChannelsLikeRust mirrors the Rust
// helper's `microphone_selection_excludes_playback_channels`: with only the
// first channel selected, playback content on the other inputs never reaches
// the mixed microphone sample.
func TestInputChannelSelectionExcludesPlaybackChannelsLikeRust(t *testing.T) {
	plan, err := newInputChannel([]uint16{1}, 16)
	if err != nil {
		t.Fatal(err)
	}
	pluggedIn := make([]int16, 16)
	pluggedIn[0] = 8192
	pluggedIn[2] = 26214
	pluggedIn[3] = 22937
	unplugged := append([]int16(nil), pluggedIn...)
	unplugged[0] = 0
	interleaved := append(append([]int16(nil), pluggedIn...), unplugged...)
	mixed := plan.mix(interleaved, 16)
	if len(mixed) != 2 || mixed[0] != 8192 || mixed[1] != 0 {
		t.Fatalf("mixed = %v, want [8192 0]", mixed)
	}
}

// TestInputChannelSelectedChannelDoesNotAttenuateLikeRust mirrors
// `selected_channel_converts_integer_samples_without_attenuation`: a single
// selected channel keeps its full amplitude.
func TestInputChannelSelectedChannelDoesNotAttenuateLikeRust(t *testing.T) {
	plan, err := newInputChannel([]uint16{2}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.sample([]int16{0, 16384}); got != 16384 {
		t.Fatalf("sample = %d, want 16384", got)
	}
}

// TestInputChannelUnsetKeepsMixingLikeRust mirrors
// `unset_channel_preserves_mixing`: an unset selection averages every channel.
func TestInputChannelUnsetKeepsMixingLikeRust(t *testing.T) {
	plan, err := newInputChannel(nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.sample([]int16{8192, 24576}); got != 16384 {
		t.Fatalf("sample = %d, want 16384", got)
	}
}

// TestInputChannelRejectsUnavailableChannelLikeRust mirrors
// `unavailable_channel_is_rejected`.
func TestInputChannelRejectsUnavailableChannelLikeRust(t *testing.T) {
	if _, err := newInputChannel([]uint16{17}, 16); !errors.Is(err, errSelectedChannelsUnavailable) {
		t.Fatalf("out-of-range selection error = %v", err)
	}
	if _, err := newInputChannel(nil, 0); !errors.Is(err, errSelectedChannelsUnavailable) {
		t.Fatalf("empty device error = %v", err)
	}
}

// TestInputChannelSubsetMixesSelectedInputsAndDeduplicatesLikeRust mirrors
// `subset_mixes_only_selected_inputs_and_deduplicates`.
func TestInputChannelSubsetMixesSelectedInputsAndDeduplicatesLikeRust(t *testing.T) {
	plan, err := newInputChannel([]uint16{2, 1, 2}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.indices) != 2 || plan.indices[0] != 0 || plan.indices[1] != 1 {
		t.Fatalf("indices = %v, want [0 1]", plan.indices)
	}
	if got := plan.sample([]int16{8192, 24576, 26214, 26214}); got != 16384 {
		t.Fatalf("sample = %d, want 16384", got)
	}
	if got := plan.sample([]int16{0, 0, 26214, 26214}); got != 0 {
		t.Fatalf("sample = %d, want 0", got)
	}
	if _, err := newInputChannel([]uint16{}, 4); !errors.Is(err, errSelectedChannelsUnavailable) {
		t.Fatalf("empty selection error = %v", err)
	}
	if _, err := newInputChannel([]uint16{1, 5}, 4); !errors.Is(err, errSelectedChannelsUnavailable) {
		t.Fatalf("partly unavailable selection error = %v", err)
	}
}

// TestInputChannelMixDropsPartialTrailingFrame pins the interleaved reduction:
// only whole frames reach the mono pipeline, matching the Rust chunking.
func TestInputChannelMixDropsPartialTrailingFrame(t *testing.T) {
	plan, err := newInputChannel([]uint16{2}, 4)
	if err != nil {
		t.Fatal(err)
	}
	whole := []int16{100, 200, 300, 400, 500, 600, 700, 800}
	if got := plan.mix(whole, 4); len(got) != 2 || got[0] != 200 || got[1] != 600 {
		t.Fatalf("mixed = %v, want [200 600]", got)
	}
	partial := append(append([]int16(nil), whole...), 900)
	if got := plan.mix(partial, 4); len(got) != 2 || got[0] != 200 || got[1] != 600 {
		t.Fatalf("partial mixed = %v, want [200 600]", got)
	}
	if got := plan.mix([]int16{1, 2, 3}, 4); got != nil {
		t.Fatalf("short buffer mixed = %v, want nil", got)
	}
}

// TestInputChannelMonoPassesThrough pins that a mono device needs no mixing.
func TestInputChannelMonoPassesThrough(t *testing.T) {
	plan, err := newInputChannel(nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	samples := []int16{1, 2, 3}
	if got := plan.mix(samples, 1); len(got) != 3 || got[2] != 3 {
		t.Fatalf("mono mix = %v", got)
	}
}
