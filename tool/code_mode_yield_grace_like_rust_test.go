package tool

import (
	"testing"
	"time"
)

// Mirrors Rust's YIELD_GRACE_PERIOD: observations whose yield time is at least
// MIN_YIELD_TIME_FOR_GRACE wait one extra second, so output published just past
// the deadline still lands in this observation.
func TestCodeModeYieldTimeoutAppliesTheGraceLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name  string
		yield int
		want  time.Duration
	}{
		{name: "immediate", yield: 0, want: 0},
		{name: "below the threshold", yield: 9999, want: 9999 * time.Millisecond},
		{name: "at the threshold", yield: 10000, want: 11 * time.Second},
		{name: "default exec yield", yield: 30000, want: 31 * time.Second},
		{name: "already capped", yield: 300000, want: 301 * time.Second},
	} {
		if got := codeModeYieldTimeout(tc.yield); got != tc.want {
			t.Fatalf("%s: codeModeYieldTimeout(%d) = %v, want %v", tc.name, tc.yield, got, tc.want)
		}
	}
}
