package tui

import "time"

// Rust parity subset: codex-rs/tui/src/tui/frame_rate_limiter.rs.

const MinFrameInterval = 8333334 * time.Nanosecond

type FrameRateLimiter struct {
	MaxFPS        int
	LastEmittedAt *time.Time
}

func (l FrameRateLimiter) ClampDeadline(requested time.Time) time.Time {
	if l.LastEmittedAt == nil {
		return requested
	}
	minAllowed := l.LastEmittedAt.Add(l.minInterval())
	if requested.Before(minAllowed) {
		return minAllowed
	}
	return requested
}

func (l *FrameRateLimiter) MarkEmitted(emittedAt time.Time) {
	if l == nil {
		return
	}
	value := emittedAt
	l.LastEmittedAt = &value
}

func (l FrameRateLimiter) minInterval() time.Duration {
	if l.MaxFPS > 0 {
		return time.Second / time.Duration(l.MaxFPS)
	}
	return MinFrameInterval
}
