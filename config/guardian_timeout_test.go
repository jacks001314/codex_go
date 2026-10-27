package config

import (
	"testing"
	"time"
)

// Rust #56371: auto_review.timeout_ms configures the overall Guardian review
// budget; zero disables the deadline.
func TestGuardianReviewTimeoutLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		values      map[string]any
		want        time.Duration
		wantLimited bool
	}{
		{name: "default", want: 90 * time.Second, wantLimited: true},
		{name: "configured", values: map[string]any{"auto_review": map[string]any{"timeout_ms": int64(1500)}}, want: 1500 * time.Millisecond, wantLimited: true},
		{name: "float value", values: map[string]any{"auto_review": map[string]any{"timeout_ms": float64(250)}}, want: 250 * time.Millisecond, wantLimited: true},
		{name: "disabled", values: map[string]any{"auto_review": map[string]any{"timeout_ms": int64(0)}}, want: 0, wantLimited: false},
		{name: "invalid", values: map[string]any{"auto_review": map[string]any{"timeout_ms": "soon"}}, want: 90 * time.Second, wantLimited: true},
		{name: "negative", values: map[string]any{"auto_review": map[string]any{"timeout_ms": int64(-1)}}, want: 90 * time.Second, wantLimited: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &Config{Values: testCase.values}
			got, limited := cfg.GuardianReviewTimeout()
			if got != testCase.want || limited != testCase.wantLimited {
				t.Fatalf("GuardianReviewTimeout() = (%v, %v), want (%v, %v)", got, limited, testCase.want, testCase.wantLimited)
			}
		})
	}
	var nilConfig *Config
	if got, limited := nilConfig.GuardianReviewTimeout(); got != 90*time.Second || !limited {
		t.Fatalf("nil config = (%v, %v)", got, limited)
	}
}
