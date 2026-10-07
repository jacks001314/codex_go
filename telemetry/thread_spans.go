package telemetry

import "sync"

// ThreadIDAttribute is the span attribute that marks a span as a thread's live
// session span. The app-server stamps it on the `session_loop` span it opens per
// loaded thread (Rust's session span carries the thread id), which is the Go
// equivalent of the ambient span Rust's tracing subscriber resolves inside a
// session.
const ThreadIDAttribute = "thread_id"

// liveThreadSpans keeps the newest live span of each thread, newest last, so a
// nested span still resolves to itself while it is open.
var liveThreadSpans struct {
	mu       sync.Mutex
	byThread map[string][]*Span
}

// registerLiveThreadSpan records a span carrying ThreadIDAttribute.
func registerLiveThreadSpan(span *Span) {
	if span == nil || span.threadID == "" {
		return
	}
	liveThreadSpans.mu.Lock()
	defer liveThreadSpans.mu.Unlock()
	if liveThreadSpans.byThread == nil {
		liveThreadSpans.byThread = map[string][]*Span{}
	}
	liveThreadSpans.byThread[span.threadID] = append(liveThreadSpans.byThread[span.threadID], span)
}

// releaseLiveThreadSpan drops a finished span from its thread's stack.
func releaseLiveThreadSpan(span *Span) {
	if span == nil || span.threadID == "" {
		return
	}
	liveThreadSpans.mu.Lock()
	defer liveThreadSpans.mu.Unlock()
	stack := liveThreadSpans.byThread[span.threadID]
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == span {
			stack = append(stack[:i], stack[i+1:]...)
			break
		}
	}
	if len(stack) == 0 {
		delete(liveThreadSpans.byThread, span.threadID)
		return
	}
	liveThreadSpans.byThread[span.threadID] = stack
}

// LiveThreadSpan returns the innermost live span of a thread, or nil when the
// thread has none. It is the lookup a trace-safe record uses when the emitting
// code path has no context to read a span from.
func LiveThreadSpan(threadID string) *Span {
	if threadID == "" {
		return nil
	}
	liveThreadSpans.mu.Lock()
	defer liveThreadSpans.mu.Unlock()
	stack := liveThreadSpans.byThread[threadID]
	if len(stack) == 0 {
		return nil
	}
	return stack[len(stack)-1]
}
