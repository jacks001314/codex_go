package turn

import (
	"context"
	"sync"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/tool"
)

// Rust parity: codex-rs/core/src/tools/parallel.rs direct-tool timing. A serial
// batch charges the whole execution interval to tools because each call depends
// on the previous one.

func criticalPathDispatcher(t *testing.T, timing *TimingState, registry *tool.Registry) *ToolDispatcher {
	t.Helper()
	return NewToolDispatcher(&ToolDispatcherOptions{
		Router:             tool.NewRouter(registry),
		ThreadID:           "thread-1",
		TurnID:             "turn-1",
		CriticalPathTiming: true,
		TimingState:        timing,
	})
}

func criticalPathTimings(t *testing.T, timing *TimingState) CriticalPathDurations {
	t.Helper()
	profile := timing.CompleteProfile(time.Now())
	if profile.CriticalPath.Status != CriticalPathStatusComplete || profile.CriticalPath.Durations == nil {
		t.Fatalf("CriticalPath = %+v, want complete durations", profile.CriticalPath)
	}
	return *profile.CriticalPath.Durations
}

func TestToolDispatcherSerialCallsShareOneCriticalPath(t *testing.T) {
	const step = 50 * time.Millisecond
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("slow_step")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		time.Sleep(step)
		return &tool.Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	timing := NewTimingState()
	timing.EnableCriticalPath()
	timing.MarkTurnStarted(time.Now())
	dispatcher := criticalPathDispatcher(t, timing, registry)
	batch := dispatcher.BatchTiming()
	if batch == nil {
		t.Fatal("BatchTiming() = nil")
	}
	// The agent loop brackets one sampling step's dispatch with a blocking guard.
	blocking := timing.BeginToolBlocking(time.Now(), batch)
	if _, err := dispatcher.ExecuteToolItemsWithBatch(context.Background(), []model.AgentItem{
		{ID: "call-1", Type: "function_call", Name: "slow_step", CallID: "call-1", Arguments: `{}`},
		{ID: "call-2", Type: "function_call", Name: "slow_step", CallID: "call-2", Arguments: `{}`},
	}, batch); err != nil {
		t.Fatalf("ExecuteToolItemsWithBatch() error = %v", err)
	}
	blocking.CloseAt(time.Now())
	completed := batch.Completed()
	if completed == nil || completed.Path == nil {
		t.Fatalf("batch.Completed() = %+v", completed)
	}
	got := criticalPathTimings(t, timing)
	stepMS := uint64(step / time.Millisecond)
	// Only the last call's own segment is `tools`; the serialized call it
	// waited on is an unattributed dependency of this step.
	if got.ToolsMS < stepMS || got.ToolsMS >= 2*stepMS {
		t.Fatalf("tools = %dms, want one step in [%d,%d)", got.ToolsMS, stepMS, 2*stepMS)
	}
	if got.UnknownMS < stepMS {
		t.Fatalf("unknown = %dms, want the serialized dependency of at least %dms", got.UnknownMS, stepMS)
	}
}

func TestToolDispatcherParallelCallsOverlapOnceInCriticalPath(t *testing.T) {
	const step = 50 * time.Millisecond
	registry := tool.NewRegistry()
	var started sync.WaitGroup
	started.Add(2)
	release := make(chan struct{})
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("parallel_step"), Parallel: true}, func(ctx context.Context, _ *tool.Invocation) (*tool.Output, error) {
		started.Done()
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		time.Sleep(step)
		return &tool.Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	timing := NewTimingState()
	timing.EnableCriticalPath()
	timing.MarkTurnStarted(time.Now())
	dispatcher := criticalPathDispatcher(t, timing, registry)
	batch := dispatcher.BatchTiming()
	blocking := timing.BeginToolBlocking(time.Now(), batch)
	go func() {
		started.Wait()
		close(release)
	}()
	if _, err := dispatcher.ExecuteToolItemsWithBatch(context.Background(), []model.AgentItem{
		{ID: "call-1", Type: "function_call", Name: "parallel_step", CallID: "call-1", Arguments: `{}`},
		{ID: "call-2", Type: "function_call", Name: "parallel_step", CallID: "call-2", Arguments: `{}`},
	}, batch); err != nil {
		t.Fatalf("ExecuteToolItemsWithBatch() error = %v", err)
	}
	blocking.CloseAt(time.Now())
	completed := batch.Completed()
	if completed == nil || completed.Path == nil {
		t.Fatalf("batch.Completed() = %+v", completed)
	}
	if got := criticalPathTimings(t, timing).ToolsMS; got >= uint64((2*step)/time.Millisecond) {
		t.Fatalf("tools = %dms, want overlapping parallel calls to count once (< %v)", got, 2*step)
	}
}

func TestToolDispatcherUnsupportedDirectPathsInvalidateCapture(t *testing.T) {
	cases := []struct {
		name       string
		invocation *tool.Invocation
	}{
		{
			name: "code mode source",
			invocation: &tool.Invocation{
				CallID:   "call-code-mode",
				ToolName: tool.PlainName("exec_command"),
				Source:   ToolTraceSourceCodeMode,
			},
		},
		{
			name: "code mode exec",
			invocation: &tool.Invocation{
				CallID:   "call-exec",
				ToolName: tool.PlainName(tool.CodeModeExecToolName),
			},
		},
		{
			name: "multi-agent spawn",
			invocation: &tool.Invocation{
				CallID:   "call-spawn",
				ToolName: tool.PlainName("spawn_agent"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			timing := NewTimingState()
			timing.EnableCriticalPath()
			timing.MarkTurnStarted(time.Now())
			dispatcher := &ToolDispatcher{criticalPathTiming: true, timingState: timing}
			if guard := dispatcher.beginDirectTiming(tc.invocation, time.Now()); guard != nil {
				t.Fatalf("beginDirectTiming() = %+v, want no attribution", guard)
			}
			if got := timing.SnapshotPath(time.Now()); got != nil {
				t.Fatalf("SnapshotPath() = %v, want an invalidated capture", got)
			}
			if got := timing.CriticalPathReportSnapshot().Status; got != CriticalPathStatusIncomplete {
				t.Fatalf("status = %q, want incomplete", got)
			}
		})
	}
}

func TestToolDispatcherCancellationCompletesTimingOnce(t *testing.T) {
	registry := tool.NewRegistry()
	running := make(chan struct{})
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("waits")}, func(ctx context.Context, _ *tool.Invocation) (*tool.Output, error) {
		close(running)
		<-ctx.Done()
		return nil, ctx.Err()
	})); err != nil {
		t.Fatal(err)
	}
	timing := NewTimingState()
	timing.EnableCriticalPath()
	timing.MarkTurnStarted(time.Now())
	dispatcher := criticalPathDispatcher(t, timing, registry)
	batch := dispatcher.BatchTiming()
	blocking := timing.BeginToolBlocking(time.Now(), batch)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-running
		cancel()
	}()
	if _, err := dispatcher.ExecuteToolItemsWithBatch(ctx, []model.AgentItem{
		{ID: "call-1", Type: "function_call", Name: "waits", CallID: "call-1", Arguments: `{}`},
	}, batch); err == nil {
		t.Fatal("ExecuteToolItemsWithBatch() error = nil, want the cancelled tool error")
	}
	blocking.CloseAt(time.Now())
	completed := batch.Completed()
	if completed == nil {
		t.Fatal("batch.Completed() = nil after cancellation")
	}
	profile := timing.CompleteProfile(time.Now())
	if second := timing.CompleteProfile(time.Now()); second != profile {
		t.Fatalf("profile changed after cancellation: %+v != %+v", second, profile)
	}
	if profile.CriticalPath.Status != CriticalPathStatusComplete {
		t.Fatalf("CriticalPath.Status = %q, want complete", profile.CriticalPath.Status)
	}
}
