package tui

import (
	"testing"
	"time"
)

// Rust #41913: pausing excludes modal wait time without losing the elapsed clock.
func TestStatusTimerPauseResumeAndResetLikeRust(t *testing.T) {
	base := time.Unix(100, 0)
	timer := NewStatusTimer(base)
	timer.PauseAt(base.Add(5 * time.Second))
	timer.PauseAt(base.Add(8 * time.Second))
	if got := timer.ElapsedAt(base.Add(10 * time.Second)); got != 5*time.Second {
		t.Fatalf("paused elapsed = %v, want 5s", got)
	}
	timer.ResumeAt(base.Add(10 * time.Second))
	timer.ResumeAt(base.Add(12 * time.Second))
	if got := timer.ElapsedAt(base.Add(13 * time.Second)); got != 8*time.Second {
		t.Fatalf("resumed elapsed = %v, want 8s", got)
	}
	timer.ResetAt(24*time.Second, base.Add(13*time.Second))
	if got := timer.ElapsedAt(base.Add(13 * time.Second)); got != 24*time.Second {
		t.Fatalf("reset elapsed = %v, want 24s", got)
	}

	// Reset while paused keeps the modal pause state.
	timer.PauseAt(base.Add(13 * time.Second))
	timer.ResetAt(0, base.Add(13*time.Second))
	if got := timer.ElapsedAt(base.Add(133 * time.Second)); got != 0 {
		t.Fatalf("paused reset elapsed = %v, want 0", got)
	}
	timer.ResumeAt(base.Add(133 * time.Second))
	if got := timer.ElapsedAt(base.Add(140 * time.Second)); got != 7*time.Second {
		t.Fatalf("post-reset elapsed = %v, want 7s", got)
	}
}
