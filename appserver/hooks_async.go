package appserver

import (
	"context"
	"sync"
)

// maxConcurrentAsyncHooks bounds background hook execution per session
// (Rust MAX_CONCURRENT_ASYNC_HOOKS).
const maxConcurrentAsyncHooks = 8

// asyncHookRuntime owns bounded background work for one thread's async hooks
// (Rust CommandHookRuntime).
type asyncHookRuntime struct {
	concurrency chan struct{}
	results     chan asyncHookResult
	done        chan struct{}
	closed      bool
	wg          sync.WaitGroup
}

type asyncHookResult struct {
	ThreadID string
	TurnID   *string
	Run      HookRunSummary
}

// scheduleAsyncHook runs a background hook with the session's concurrency
// limit. Results are delivered through the thread's result channel so the
// caller can drain them at safe turn boundaries (Rust schedule_async_hook).
func (r *HookRunner) scheduleAsyncHook(ctx context.Context, request *HookRunRequest, metadata HookMetadata) {
	if r == nil || request == nil {
		return
	}
	rt := r.asyncRuntimeFor(request.ThreadID)
	rt.concurrency <- struct{}{}

	started := hookSummaryWithRunIDSuffix(runningHookSummary(metadata, r.now()), request.RunIDSuffix)
	r.notify(NotificationHookStarted, &HookRunStartedNotification{
		ThreadID: request.ThreadID,
		TurnID:   cloneString(request.TurnID),
		Run:      started,
	})

	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		defer func() { <-rt.concurrency }()
		select {
		case <-rt.done:
			return
		default:
		}
		runResult := r.runCommand(ctx, metadata, request.InputJSON, request.CWD)
		completed := completedHookSummary(metadata, runResult, hookRunStatus(request.EventName, runResult), hookOutputEntries(request.EventName, runResult))
		completed = hookSummaryWithRunIDSuffix(completed, request.RunIDSuffix)
		select {
		case rt.results <- asyncHookResult{ThreadID: request.ThreadID, TurnID: cloneString(request.TurnID), Run: completed}:
		case <-rt.done:
		}
	}()
}

func (r *HookRunner) asyncRuntimeFor(threadID string) *asyncHookRuntime {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rt, ok := r.asyncRuntimes[threadID]; ok {
		return rt
	}
	rt := &asyncHookRuntime{
		concurrency: make(chan struct{}, maxConcurrentAsyncHooks),
		results:     make(chan asyncHookResult, 64),
		done:        make(chan struct{}),
	}
	r.asyncRuntimes[threadID] = rt
	return rt
}

// DrainAsyncResults processes finished async hook results at a safe turn
// boundary (Rust drain_async_hook_results). It returns completed runs so the
// caller can record additional context before the next user prompt.
func (r *HookRunner) DrainAsyncResults(threadID string) []asyncHookResult {
	if r == nil || threadID == "" {
		return nil
	}
	r.mu.Lock()
	rt, ok := r.asyncRuntimes[threadID]
	r.mu.Unlock()
	if !ok {
		return nil
	}
	var out []asyncHookResult
	for {
		select {
		case result := <-rt.results:
			r.notify(NotificationHookCompleted, &HookRunCompletedNotification{
				ThreadID: result.ThreadID,
				TurnID:   cloneString(result.TurnID),
				Run:      result.Run,
			})
			out = append(out, result)
		default:
			return out
		}
	}
}

// ShutdownAsync aborts outstanding background hook work for a thread
// (Rust CommandHookRuntime::shutdown).
func (r *HookRunner) ShutdownAsync(threadID string) {
	if r == nil || threadID == "" {
		return
	}
	r.mu.Lock()
	rt, ok := r.asyncRuntimes[threadID]
	if ok && !rt.closed {
		rt.closed = true
		close(rt.done)
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	// Wait for in-flight background hooks to observe the shutdown and release
	// their concurrency permits (Rust CommandHookRuntime::shutdown).
	rt.wg.Wait()
}
