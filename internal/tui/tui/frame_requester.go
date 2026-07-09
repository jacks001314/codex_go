package tui

import "time"

// Rust parity subset: codex-rs/tui/src/tui/frame_requester.rs.

type FrameRequester struct {
	Pending      bool
	NextDeadline *time.Time
	Limiter      FrameRateLimiter
	DrawsEmitted int
}

func NewFrameRequester() *FrameRequester {
	return &FrameRequester{}
}

func (r *FrameRequester) ScheduleFrame(now time.Time) {
	if r == nil {
		return
	}
	r.ScheduleFrameAt(now)
}

func (r *FrameRequester) ScheduleFrameIn(now time.Time, delay time.Duration) {
	if r == nil {
		return
	}
	r.ScheduleFrameAt(now.Add(delay))
}

func (r *FrameRequester) ScheduleFrameAt(drawAt time.Time) {
	if r == nil {
		return
	}
	deadline := r.Limiter.ClampDeadline(drawAt)
	r.Pending = true
	if r.NextDeadline == nil || deadline.Before(*r.NextDeadline) {
		value := deadline
		r.NextDeadline = &value
	}
}

func (r *FrameRequester) Advance(now time.Time) bool {
	if r == nil || !r.Pending || r.NextDeadline == nil || now.Before(*r.NextDeadline) {
		return false
	}
	emittedAt := *r.NextDeadline
	r.Pending = false
	r.NextDeadline = nil
	r.DrawsEmitted++
	r.Limiter.MarkEmitted(emittedAt)
	return true
}
