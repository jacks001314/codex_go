package appserver

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"codex_go/config"
	"codex_go/session"
)

const (
	// threadUnloadCleanupWait bounds how long a resume/turn start waits for an
	// in-flight idle eviction before reporting that the thread is closing.
	threadUnloadCleanupWait = 10 * time.Second
)

// threadIdleLifecycle schedules delayed eviction for idle, unsubscribed
// multi-agent v2 children. Rust #52081/#52034 keep ephemeral children and
// running threads resident, wait for an earlier runtime's cleanup before
// attaching a listener, and preserve unread mail/environment selections across
// reload.
type threadIdleLifecycle struct {
	mu         sync.Mutex
	timers     map[string]*time.Timer
	generation map[string]uint64
	pending    map[string]chan struct{}
	pins       map[string]int
}

const maxThreadUnloadDelaySeconds = uint64(math.MaxInt64 / int64(time.Second))

// threadUnloadDelayFromConfig parses the per-thread
// `thread_unload_delay_secs` override (Rust #55270). Negative, fractional,
// non-numeric, or unschedulable values are rejected.
func threadUnloadDelayFromConfig(config map[string]any) (time.Duration, bool, error) {
	if config == nil {
		return 0, false, nil
	}
	raw, ok := config["thread_unload_delay_secs"]
	if !ok || raw == nil {
		return 0, false, nil
	}
	var seconds uint64
	switch value := raw.(type) {
	case int:
		if value < 0 {
			return 0, true, fmt.Errorf("`thread_unload_delay_secs` must be a non-negative integer")
		}
		seconds = uint64(value)
	case int64:
		if value < 0 {
			return 0, true, fmt.Errorf("`thread_unload_delay_secs` must be a non-negative integer")
		}
		seconds = uint64(value)
	case float64:
		if value < 0 || math.Trunc(value) != value || value > float64(maxThreadUnloadDelaySeconds) {
			return 0, true, fmt.Errorf("`thread_unload_delay_secs` must be a non-negative integer")
		}
		seconds = uint64(value)
	case json.Number:
		parsed, err := strconv.ParseUint(string(value), 10, 64)
		if err != nil {
			return 0, true, fmt.Errorf("`thread_unload_delay_secs` must be a non-negative integer")
		}
		seconds = parsed
	default:
		return 0, true, fmt.Errorf("`thread_unload_delay_secs` must be a non-negative integer")
	}
	if seconds > maxThreadUnloadDelaySeconds {
		return 0, true, fmt.Errorf("`thread_unload_delay_secs` is too large")
	}
	return time.Duration(seconds) * time.Second, true, nil
}

func (r *RuntimeRouter) setThreadUnloadDelayOverride(threadID string, delay time.Duration, present bool) {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	threadID = strings.TrimSpace(threadID)
	r.threadUnloadDelayMu.Lock()
	if !present {
		delete(r.threadUnloadDelays, threadID)
		r.threadUnloadDelayMu.Unlock()
		return
	}
	if r.threadUnloadDelays == nil {
		r.threadUnloadDelays = map[string]time.Duration{}
	}
	r.threadUnloadDelays[threadID] = delay
	r.threadUnloadDelayMu.Unlock()
}

func (r *RuntimeRouter) threadUnloadDelayOverride(threadID string) (time.Duration, bool) {
	if r == nil {
		return 0, false
	}
	r.threadUnloadDelayMu.Lock()
	defer r.threadUnloadDelayMu.Unlock()
	delay, ok := r.threadUnloadDelays[strings.TrimSpace(threadID)]
	return delay, ok
}

// threadConfigWithoutUnloadDelay removes the runtime-retention override before
// the thread config is persisted, so a cold resume uses the server default.
func threadConfigWithoutUnloadDelay(config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	cloned := cloneAnyMap(config)
	delete(cloned, "thread_unload_delay_secs")
	return cloned
}

// applyThreadUnloadDelayFromRequest records the runtime retention override for
// the thread a successful start/fork/resume created. The override is deliberately
// not persisted with the thread config, so a cold resume returns to the default.
func (r *RuntimeRouter) applyThreadUnloadDelayFromRequest(request *Request, result any) {
	if r == nil || request == nil || len(request.Params) == 0 {
		return
	}
	var params struct {
		ThreadID string         `json:"threadId"`
		Config   map[string]any `json:"config"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return
	}
	threadID := strings.TrimSpace(params.ThreadID)
	switch request.Method {
	case MethodThreadStart:
		if response, ok := result.(*ThreadStartResponse); ok && response != nil && response.Thread != nil {
			threadID = strings.TrimSpace(response.Thread.ID)
		}
	case MethodThreadFork:
		if response, ok := result.(*ThreadForkResponse); ok && response != nil && response.Thread != nil {
			threadID = strings.TrimSpace(response.Thread.ID)
		}
	}
	if threadID == "" {
		return
	}
	delay, present, err := threadUnloadDelayFromConfig(params.Config)
	if err != nil {
		return
	}
	r.setThreadUnloadDelayOverride(threadID, delay, present)
}

func (l *threadIdleLifecycle) ensureMapsLocked() {
	if l.timers == nil {
		l.timers = map[string]*time.Timer{}
	}
	if l.generation == nil {
		l.generation = map[string]uint64{}
	}
	if l.pending == nil {
		l.pending = map[string]chan struct{}{}
	}
	if l.pins == nil {
		l.pins = map[string]int{}
	}
}

// noteThreadActivity cancels a pending idle timer and invalidates a timer that
// is already firing.
func (r *RuntimeRouter) noteThreadActivity(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.idleLifecycle.mu.Lock()
	r.idleLifecycle.ensureMapsLocked()
	if timer := r.idleLifecycle.timers[threadID]; timer != nil {
		timer.Stop()
		delete(r.idleLifecycle.timers, threadID)
	}
	r.idleLifecycle.generation[threadID]++
	r.idleLifecycle.mu.Unlock()
}

// stopThreadIdleLifecycle cancels armed timers when the router shuts down.
// An in-flight cleanup is allowed to finish; only new evictions stop.
func (r *RuntimeRouter) stopThreadIdleLifecycle() {
	if r == nil {
		return
	}
	r.idleLifecycle.mu.Lock()
	for threadID, timer := range r.idleLifecycle.timers {
		timer.Stop()
		delete(r.idleLifecycle.timers, threadID)
	}
	r.idleLifecycle.pending = map[string]chan struct{}{}
	r.idleLifecycle.pins = map[string]int{}
	r.idleLifecycle.mu.Unlock()
}

// scheduleThreadIdleUnload arms the one-shot unload timer for an idle thread.
// Running threads, subscribed threads, persistent roots, ephemeral children and
// pinned threads are never scheduled.
func (r *RuntimeRouter) scheduleThreadIdleUnload(threadID string) {
	if r == nil || r.threads == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || !r.idleThreadEvictable(threadID) {
		return
	}
	if len(r.threads.Subscribers(threadID)) > 0 || r.threads.ActiveTurn(threadID) != nil {
		return
	}
	// An idle thread with no client attached is the Rust #48812 prewarm point:
	// prepare its WebSocket with the existing history before the unload timer
	// eventually releases the runtime.
	r.idleLifecycle.mu.Lock()
	_, unloading := r.idleLifecycle.pending[threadID]
	r.idleLifecycle.mu.Unlock()
	if !unloading {
		r.scheduleHistoryPrewarm(threadID)
	}
	delay := r.threadIdleUnloadDelay(threadID)
	// Give the start/subscribe handoff a short grace period; a zero configured
	// delay should not evict a runtime between retain and the caller's
	// subscription or first input.
	if delay < 50*time.Millisecond {
		delay = 50 * time.Millisecond
	}
	r.idleLifecycle.mu.Lock()
	r.idleLifecycle.ensureMapsLocked()
	if r.idleLifecycle.pins[threadID] > 0 {
		r.idleLifecycle.mu.Unlock()
		return
	}
	if _, pending := r.idleLifecycle.pending[threadID]; pending {
		r.idleLifecycle.mu.Unlock()
		return
	}
	if timer := r.idleLifecycle.timers[threadID]; timer != nil {
		timer.Stop()
	}
	generation := r.idleLifecycle.generation[threadID] + 1
	r.idleLifecycle.generation[threadID] = generation
	r.idleLifecycle.timers[threadID] = time.AfterFunc(delay, func() {
		r.unloadIdleChildThread(threadID, generation)
	})
	r.idleLifecycle.mu.Unlock()
}

// idleThreadEvictable reports whether local idle eviction may touch the thread.
// Persistent roots stay host-controlled and ephemeral children must retain their
// model context for follow-up tasks, but an ephemeral client root has no durable
// record to resume and is reclaimed like any other owner-less thread (Rust
// #50380/#52081). Internal sessions always carry a parent and keep their
// explicit-delay rule.
func (r *RuntimeRouter) idleThreadEvictable(threadID string) bool {
	if r == nil {
		return false
	}
	if r.threads == nil || r.threads.LiveThread(session.ThreadID(threadID)) == nil {
		return false
	}
	record, err := r.threadRecord(session.ThreadID(threadID), false, false)
	if err != nil || record == nil {
		return false
	}
	if _, ephemeral := r.ephemeralThreadRecord(session.ThreadID(threadID), false); ephemeral {
		if !runtimeRecordIsSubagent(record) {
			if strings.TrimSpace(string(record.ParentThreadID)) == "" {
				return true
			}
			// Rust #55270 allows an explicit per-thread delay (typically zero
			// for prediction threads).
			_, hasOverride := r.threadUnloadDelayOverride(threadID)
			return hasOverride
		}
		// Rust #52081 keeps ephemeral children resident.
		return false
	}
	if strings.TrimSpace(string(record.ParentThreadID)) == "" {
		return false
	}
	return true
}

func (r *RuntimeRouter) threadIdleUnloadDelay(threadID string) time.Duration {
	if delay, ok := r.threadUnloadDelayOverride(threadID); ok {
		return delay
	}
	cfg := &config.Config{Values: map[string]any{}}
	if r != nil && r.services.Config != nil {
		params := &config.ConfigReadParams{}
		if record, err := r.threadRecord(session.ThreadID(threadID), false, false); err == nil && record != nil {
			if cwd := strings.TrimSpace(record.Metadata.CWD); cwd != "" {
				params.CWD = &cwd
			}
		}
		if read, err := r.services.Config.Read(params); err == nil && read != nil {
			cfg = &config.Config{Values: read.Config}
		}
	}
	return cfg.ThreadUnloadDelaySecs()
}

// pinThreadResidency blocks idle eviction until the returned release runs. Turn
// submission holds a pin so a freshly reloaded child cannot be evicted before
// it receives its work (Rust #52034).
func (r *RuntimeRouter) pinThreadResidency(threadID string) func() {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return func() {}
	}
	r.idleLifecycle.mu.Lock()
	r.idleLifecycle.ensureMapsLocked()
	r.idleLifecycle.pins[threadID]++
	r.idleLifecycle.mu.Unlock()
	return r.releaseThreadResidencyPin(threadID)
}

// releaseThreadResidencyPin drops one residency pin, re-arming the idle unload
// timer once the thread has no pins left.
func (r *RuntimeRouter) releaseThreadResidencyPin(threadID string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			r.idleLifecycle.mu.Lock()
			if r.idleLifecycle.pins[threadID] > 0 {
				r.idleLifecycle.pins[threadID]--
			}
			idle := r.idleLifecycle.pins[threadID] == 0
			r.idleLifecycle.mu.Unlock()
			if idle {
				r.scheduleThreadIdleUnload(threadID)
			}
		})
	}
}

// acquireThreadResidencyPin pins a thread for the whole request and waits out an
// idle eviction that already started. The pin and the pending entry are read
// under the same lock, so once the pin is held no new eviction can claim the
// thread (Rust #57787 serializes cold resume and listener install against the
// thread's unload by locking the thread state). Without the pin a cold resume
// could install its runtime, then lose it to a stale idle-unload timer before
// the subscribing connection was attached.
func (r *RuntimeRouter) acquireThreadResidencyPin(threadID string) (func(), error) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return func() {}, nil
	}
	for {
		r.idleLifecycle.mu.Lock()
		r.idleLifecycle.ensureMapsLocked()
		done := r.idleLifecycle.pending[threadID]
		if done == nil {
			r.idleLifecycle.pins[threadID]++
			r.idleLifecycle.mu.Unlock()
			return r.releaseThreadResidencyPin(threadID), nil
		}
		r.idleLifecycle.mu.Unlock()
		select {
		case <-done:
		case <-time.After(threadUnloadCleanupWait):
			return nil, fmt.Errorf("%w: thread %s is closing; retry after the thread is closed", session.ErrConflict, threadID)
		}
	}
}

// awaitPendingThreadUnload lets a resume/turn start attach only after an
// earlier runtime finished cleanup. A stuck cleanup reports the Rust closing
// error instead of running two runtimes for the same thread.
func (r *RuntimeRouter) awaitPendingThreadUnload(threadID string) error {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	r.idleLifecycle.mu.Lock()
	done := r.idleLifecycle.pending[threadID]
	r.idleLifecycle.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-time.After(threadUnloadCleanupWait):
		return fmt.Errorf("%w: thread %s is closing; retry after the thread is closed", session.ErrConflict, threadID)
	}
}

func (r *RuntimeRouter) unloadIdleChildThread(threadID string, generation uint64) {
	if r == nil || r.threads == nil {
		return
	}
	r.idleLifecycle.mu.Lock()
	r.idleLifecycle.ensureMapsLocked()
	if r.idleLifecycle.generation[threadID] != generation {
		r.idleLifecycle.mu.Unlock()
		return
	}
	delete(r.idleLifecycle.timers, threadID)
	if _, pending := r.idleLifecycle.pending[threadID]; pending {
		r.idleLifecycle.mu.Unlock()
		return
	}
	if r.idleLifecycle.pins[threadID] > 0 || !r.idleThreadEvictable(threadID) ||
		len(r.threads.Subscribers(threadID)) > 0 || r.threads.ActiveTurn(threadID) != nil {
		r.idleLifecycle.mu.Unlock()
		return
	}
	done := make(chan struct{})
	r.idleLifecycle.pending[threadID] = done
	dropEphemeralRecord := r.idleEphemeralRootThread(threadID)
	r.idleLifecycle.mu.Unlock()

	r.markThreadUnloaded(threadID)
	r.threads.ReleaseLiveThreads([]session.ThreadID{session.ThreadID(threadID)})
	if dropEphemeralRecord {
		// An ephemeral client root has no rollout, so Rust's closed thread is
		// gone rather than resumable from an in-memory copy.
		r.threads.DeleteEphemeralRecord(session.ThreadID(threadID))
	}
	// Rust #52081/#50380: a completed idle eviction broadcasts thread/closed,
	// so clients drop the runtime they no longer have a subscriber for.
	r.notify(NotificationThreadClosed, ThreadClosedNotification{ThreadID: threadID})

	r.idleLifecycle.mu.Lock()
	delete(r.idleLifecycle.pending, threadID)
	close(done)
	r.idleLifecycle.mu.Unlock()
}

// idleEphemeralRootThread reports whether threadID is an ephemeral client root:
// no parent, no subagent owner, and no internal host registration.
func (r *RuntimeRouter) idleEphemeralRootThread(threadID string) bool {
	if r == nil || r.threads == nil {
		return false
	}
	record, ok := r.ephemeralThreadRecord(session.ThreadID(threadID), false)
	if !ok || record == nil || runtimeRecordIsSubagent(record) {
		return false
	}
	if strings.TrimSpace(string(record.ParentThreadID)) != "" {
		return false
	}
	return !r.isInternalSessionThread(threadID)
}

// threadLifecycleRequestThreadID extracts the thread a lifecycle-sensitive
// request operates on. Thread starts have no thread id yet.
func threadLifecycleRequestThreadID(request *Request) string {
	if request == nil || len(request.Params) == 0 {
		return ""
	}
	var params struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return ""
	}
	return strings.TrimSpace(params.ThreadID)
}

func threadLifecycleSensitiveMethod(method Method) bool {
	switch method {
	case MethodThreadResume, MethodThreadFork, MethodTurnStart, MethodThreadCompactStart:
		return true
	default:
		return false
	}
}
