
package model

import (
	"testing"
	"time"
)

// Mirrors Rust #46540 codex_async_utils::backoff: 200 ms doubling per attempt
// with up to 10% jitter, server advice taking precedence, and no fixed ceiling
// (callers enforce the retry budget).
func TestResponsesRetryDelayMatchesRustBackoff(t *testing.T) {
	bounds := func(base time.Duration) (time.Duration, time.Duration) {
		return time.Duration(float64(base) * 0.9), time.Duration(float64(base) * 1.1)
	}
	for attempt, base := range map[uint64]time.Duration{
		0: 200 * time.Millisecond,
		1: 200 * time.Millisecond,
		2: 400 * time.Millisecond,
		3: 800 * time.Millisecond,
		6: 6400 * time.Millisecond,
	} {
		low, high := bounds(base)
		for i := 0; i < 32; i++ {
			delay := responsesRetryDelay(nil, attempt)
			if delay < low || delay > high {
				t.Fatalf("attempt %d delay = %v, want within [%v, %v]", attempt, delay, low, high)
			}
		}
	}
	if delay := responsesRetryDelay(nil, 6); delay <= 5*time.Second {
		t.Fatalf("attempt 6 delay = %v, want Rust's uncapped backoff", delay)
	}
}
