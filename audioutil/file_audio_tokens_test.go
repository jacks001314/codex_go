package audioutil

import "testing"

func floatPtrForAudioBudgetTest(value float64) *float64 { return &value }

// TestEstimateFileAudioTokensLikeRust pins Rust estimate_file_audio_tokens
// (#59268): measured durations round up at 10 tokens per second, are capped at
// the 20-minute Responses maximum, and an unknown/zero duration reserves the
// whole maximum budget.
func TestEstimateFileAudioTokensLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		duration *float64
		want     int
	}{
		{name: "unknown", duration: nil, want: 12000},
		{name: "zero", duration: floatPtrForAudioBudgetTest(0), want: 12000},
		{name: "negative", duration: floatPtrForAudioBudgetTest(-1), want: 12000},
		{name: "rounds up", duration: floatPtrForAudioBudgetTest(0.05), want: 1},
		{name: "whole second", duration: floatPtrForAudioBudgetTest(1), want: 10},
		{name: "fractional", duration: floatPtrForAudioBudgetTest(1.21), want: 13},
		{name: "caps at maximum", duration: floatPtrForAudioBudgetTest(30 * 60), want: 12000},
		{name: "exactly maximum", duration: floatPtrForAudioBudgetTest(20 * 60), want: 12000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EstimateFileAudioTokens(tc.duration); got != tc.want {
				t.Fatalf("EstimateFileAudioTokens(%v) = %d, want %d", tc.duration, got, tc.want)
			}
		})
	}
	if MaxFileAudioTokens != 12000 {
		t.Fatalf("MaxFileAudioTokens = %d, want 12000", MaxFileAudioTokens)
	}
}
