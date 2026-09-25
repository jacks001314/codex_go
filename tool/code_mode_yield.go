package tool

import "sync"

// YieldSignal mirrors the preempt cancellation token Rust adds to
// `CodeModeSession::execute`/`wait` (#48123): cancelling it ends the current
// observation early while its cell keeps running and stays available to later
// waits. A nil signal means "never preempt".
type YieldSignal struct {
	once sync.Once
	done chan struct{}
}

// NewYieldSignal returns an uncancelled signal.
func NewYieldSignal() *YieldSignal {
	return &YieldSignal{done: make(chan struct{})}
}

// Done reports the channel closed when the signal fires. A nil signal returns a
// nil channel, which blocks forever in a select, so callers can use it directly.
func (s *YieldSignal) Done() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.done
}

// Cancel fires the signal once.
func (s *YieldSignal) Cancel() {
	if s == nil {
		return
	}
	s.once.Do(func() { close(s.done) })
}

// Cancelled reports whether the signal already fired.
func (s *YieldSignal) Cancelled() bool {
	if s == nil {
		return false
	}
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// CodeModePreemptContextKey carries the host's per-request yield signal into the
// code-mode exec/wait executors, so a host `operation/yield` frame can end the
// observation early (Rust #48123's preempt signal).
const CodeModePreemptContextKey = "code_mode_preempt_signal"

// YieldSignalFromInvocation reads the invocation's preempt signal.
func YieldSignalFromInvocation(invocation *Invocation) *YieldSignal {
	if invocation == nil || invocation.Context == nil {
		return nil
	}
	signal, _ := invocation.Context[CodeModePreemptContextKey].(*YieldSignal)
	return signal
}
