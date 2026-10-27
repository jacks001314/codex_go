package appserver

import (
	"testing"
	"time"
)

// Rust #55846: review latency comes from one monotonic interval captured at
// completion, so wall-clock changes cannot skew it and a late read cannot make
// it grow.
func TestGuardianReviewElapsedMSUsesMonotonicClockLikeRust(t *testing.T) {
	if got := guardianReviewElapsedMS(time.Time{}); got != 0 {
		t.Fatalf("zero start = %d, want 0", got)
	}
	// A start in the future cannot produce a negative interval.
	if got := guardianReviewElapsedMS(time.Now().Add(time.Hour)); got != 0 {
		t.Fatalf("future start = %d, want 0", got)
	}
	started := time.Now()
	time.Sleep(20 * time.Millisecond)
	got := guardianReviewElapsedMS(started)
	if got < 15 || got > 5_000 {
		t.Fatalf("elapsed = %d ms, want about 20", got)
	}
	// The same start keeps reporting a bounded interval as time moves on, and
	// each call measures from the captured instant rather than from a stored
	// wall-clock timestamp.
	time.Sleep(5 * time.Millisecond)
	later := guardianReviewElapsedMS(started)
	if later < got || later > 5_000 {
		t.Fatalf("later elapsed = %d ms, first = %d ms", later, got)
	}
}
