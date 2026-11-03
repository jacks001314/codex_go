package tea

import (
	"sync"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// frameRequestMsg is the animation frame notification delivered to the event
// loop. Consuming it makes Bubble Tea render, which is the draw that Rust's
// FrameRequester schedules.
type frameRequestMsg struct{}

// frameRequester mirrors tui/src/tui/frame_requester.rs: widgets ask for the
// next frame, a scheduler coalesces the requests to the earliest deadline, and
// only that deadline produces a draw. Without a Program (tests, embedders) the
// requester stays detached and the command-driven tick chain keeps working.
type frameRequester struct {
	mu       sync.Mutex
	send     func(bubbletea.Msg)
	timer    *time.Timer
	deadline time.Time
}

// attach connects the requester to the Program's message loop.
func (r *frameRequester) attach(send func(bubbletea.Msg)) {
	if r == nil || send == nil {
		return
	}
	r.mu.Lock()
	r.send = send
	r.mu.Unlock()
}

// attached reports whether frames can be delivered.
func (r *frameRequester) attached() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.send != nil
}

// requestAnim schedules one frame after delay. A request that is later than the
// pending deadline is coalesced away, matching FrameScheduler::run.
func (r *frameRequester) requestAnim(delay time.Duration) {
	if r == nil || delay < 0 {
		return
	}
	r.mu.Lock()
	if r.send == nil {
		r.mu.Unlock()
		return
	}
	deadline := time.Now().Add(delay)
	if r.timer != nil {
		if !r.deadline.After(deadline) {
			r.mu.Unlock()
			return
		}
		r.timer.Stop()
	}
	r.deadline = deadline
	r.timer = time.AfterFunc(delay, r.fire)
	r.mu.Unlock()
}

func (r *frameRequester) fire() {
	r.mu.Lock()
	send := r.send
	r.timer = nil
	r.deadline = time.Time{}
	r.mu.Unlock()
	if send != nil {
		send(frameRequestMsg{})
	}
}

// stop drops the pending request, used when nothing animates any more.
func (r *frameRequester) stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	r.deadline = time.Time{}
	r.mu.Unlock()
}
