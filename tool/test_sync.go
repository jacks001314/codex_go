package tool

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// TestSyncHandler mirrors Rust core/src/tools/handlers/test_sync.rs: a test
// coordination tool exposed only to models that declare `test_sync_tool` in
// their experimental_supported_tools. It sleeps for optional delays and/or
// waits on a named barrier shared by concurrent tool calls, returning "ok".
type TestSyncHandler struct {
	mu       sync.Mutex
	barriers map[string]*testSyncBarrier
}

type testSyncBarrier struct {
	ready        chan struct{}
	participants int
	arrived      int
	released     bool
}

type testSyncBarrierArgs struct {
	ID           string `json:"id"`
	Participants int    `json:"participants"`
	TimeoutMS    int    `json:"timeout_ms"`
}

type testSyncArgs struct {
	SleepBeforeMS *int                 `json:"sleep_before_ms"`
	SleepAfterMS  *int                 `json:"sleep_after_ms"`
	Barrier       *testSyncBarrierArgs `json:"barrier"`
}

const testSyncDefaultTimeoutMS = 1000

// Spec mirrors Rust create_test_sync_tool (test_sync_spec.rs).
func (h *TestSyncHandler) Spec() Spec {
	properties := map[string]any{
		"sleep_before_ms": map[string]any{"type": "integer", "description": "Delay before any other action. Defaults to no delay."},
		"sleep_after_ms":  map[string]any{"type": "integer", "description": "Delay after any other action. Defaults to no delay."},
		"barrier": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":           map[string]any{"type": "string", "description": "Identifier shared by concurrent calls that should rendezvous"},
				"participants": map[string]any{"type": "integer", "description": "Number of tool calls that must arrive before the barrier opens"},
				"timeout_ms":   map[string]any{"type": "integer", "description": "Maximum barrier wait in milliseconds. Defaults to 1000."},
			},
			"required": []string{"id", "participants"},
		},
	}
	return Spec{
		Name:        PlainName("test_sync_tool"),
		Description: "Synchronizes concurrent tool calls for testing.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": properties,
		},
		Parallel: true,
	}
}

func (h *TestSyncHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	if invocation == nil {
		return nil, fmt.Errorf("test_sync_tool handler received unsupported payload")
	}
	var args testSyncArgs
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	if args.SleepBeforeMS != nil && *args.SleepBeforeMS > 0 {
		if err := sleepContext(ctx, time.Duration(*args.SleepBeforeMS)*time.Millisecond); err != nil {
			return nil, err
		}
	}
	if args.Barrier != nil {
		if err := h.waitOnBarrier(ctx, *args.Barrier); err != nil {
			return nil, err
		}
	}
	if args.SleepAfterMS != nil && *args.SleepAfterMS > 0 {
		if err := sleepContext(ctx, time.Duration(*args.SleepAfterMS)*time.Millisecond); err != nil {
			return nil, err
		}
	}
	return &Output{Success: true, Body: "ok"}, nil
}

func (h *TestSyncHandler) waitOnBarrier(ctx context.Context, args testSyncBarrierArgs) error {
	if args.Participants <= 0 {
		return fmt.Errorf("barrier participants must be greater than zero")
	}
	timeoutMS := args.TimeoutMS
	if timeoutMS == 0 {
		timeoutMS = testSyncDefaultTimeoutMS
	}
	if timeoutMS < 0 {
		return fmt.Errorf("barrier timeout must be greater than zero")
	}
	barrier := h.getOrCreateBarrier(args.ID, args.Participants)
	if barrier == nil {
		return fmt.Errorf("barrier %s already registered with different participants", args.ID)
	}
	h.arrive(barrier)
	timer := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("test_sync_tool barrier wait timed out")
	case <-barrier.ready:
		return nil
	}
}

func (h *TestSyncHandler) getOrCreateBarrier(id string, participants int) *testSyncBarrier {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.barriers == nil {
		h.barriers = map[string]*testSyncBarrier{}
	}
	if existing, ok := h.barriers[id]; ok {
		if existing.participants != participants {
			return nil
		}
		return existing
	}
	barrier := &testSyncBarrier{
		ready:        make(chan struct{}),
		participants: participants,
	}
	h.barriers[id] = barrier
	return barrier
}

// arrive counts one caller and releases all waiters when the last participant
// arrives (mirrors Rust tokio Barrier::wait).
func (h *TestSyncHandler) arrive(barrier *testSyncBarrier) {
	h.mu.Lock()
	barrier.arrived++
	release := barrier.arrived >= barrier.participants
	if release {
		barrier.released = true
		close(barrier.ready)
	}
	h.mu.Unlock()
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
