package chatwidget

import (
	"math"
	"testing"
	"time"
)

// Rust #42372 app/rate_limit_refresh.rs: the poll interval tightens as the
// window fills, and only the aggregate or current-model snapshots count.
func TestRateLimitRefreshIntervalLikeRust(t *testing.T) {
	window := func(percent float64) *RateLimitWindow { return &RateLimitWindow{UsedPercent: percent} }
	cases := []struct {
		name      string
		snapshots map[string]RateLimitSnapshot
		model     string
		want      time.Duration
	}{
		{name: "empty keeps the idle cadence", snapshots: nil, want: 60 * time.Second},
		{name: "75 tightens to 30s", snapshots: map[string]RateLimitSnapshot{"codex": {Primary: window(75)}}, want: 30 * time.Second},
		{name: "90 tightens to 15s", snapshots: map[string]RateLimitSnapshot{"codex": {Secondary: window(90)}}, want: 15 * time.Second},
		{name: "99 tightens to 5s", snapshots: map[string]RateLimitSnapshot{"codex": {Primary: window(99)}}, want: 5 * time.Second},
		{name: "other limits do not count", snapshots: map[string]RateLimitSnapshot{"gpt-other": {Primary: window(99)}}, model: "gpt-5", want: 60 * time.Second},
		{name: "current model limit counts", snapshots: map[string]RateLimitSnapshot{"gpt-reserve": {Primary: window(92)}}, model: "gpt-reserve", want: 15 * time.Second},
		{name: "highest window wins", snapshots: map[string]RateLimitSnapshot{"codex": {Primary: window(10), Secondary: window(80)}}, want: 30 * time.Second},
		{name: "nan is ignored", snapshots: map[string]RateLimitSnapshot{"codex": {Primary: window(math.NaN())}}, want: 60 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RateLimitRefreshInterval(tc.snapshots, tc.model); got != tc.want {
				t.Fatalf("RateLimitRefreshInterval() = %v, want %v", got, tc.want)
			}
		})
	}
}
