package codemode

import (
	"context"
	"testing"
	"time"
)

// Mirrors Rust's resolve_yield_timeout ordering: the grace applies first, then
// the session's max_yield_time_ms caps the result. A zero limit therefore makes
// even a ten-second observation return at once.
func TestSessionRuntimeHonorsASessionLimitAfterTheGraceLikeRust(t *testing.T) {
	engine := newBlockingEngine()
	runtime := newPreemptRuntime(engine)
	zeroLimit := uint64(0)
	runtime.SetCellExecutionLimits(&CellExecutionLimits{MaxYieldTimeMS: &zeroLimit})

	began := time.Now()
	started, err := runtime.Execute(context.Background(), &ExecuteRequest{
		ToolCallID: "limited-exec", Source: "text('released')", YieldTimeMS: uint64Ptr(10000),
	}, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if started.InitialResponse.Variant != "Yielded" {
		t.Fatalf("limited execute response = %#v", started.InitialResponse)
	}
	wait, err := runtime.Wait(context.Background(), &WaitRequest{CellID: started.CellID, YieldTimeMS: 10000}, nil)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if elapsed := time.Since(began); elapsed > 2*time.Second {
		t.Fatalf("capped observations blocked for %v", elapsed)
	}
	if wait.Response.Variant != "Yielded" {
		t.Fatalf("limited wait response = %#v", wait.Response)
	}
	close(engine.release)
}

func TestSessionHostCapturesTheLimitsForItsRuntimeLikeRust(t *testing.T) {
	runtime := NewSessionRuntime()
	host := NewSessionHost(runtime)
	ctx := context.Background()
	sessionID, _ := NewSessionID("session-grace")
	limit := uint64(10500)
	open := OpenSessionRequestWithLimits(sessionID, &CellExecutionLimits{MaxYieldTimeMS: &limit})
	if _, _, err := host.Handle(ctx, &open, nil); err != nil {
		t.Fatalf("Handle(open) error = %v", err)
	}
	// The grace is applied before the cap, so a limit just above ten seconds
	// absorbs the extra second (Rust #57272).
	if got, want := runtime.yieldTimeout(10000), 10500*time.Millisecond; got != want {
		t.Fatalf("yieldTimeout(10000) = %v, want %v", got, want)
	}
	if got, want := runtime.yieldTimeout(9999), 9999*time.Millisecond; got != want {
		t.Fatalf("yieldTimeout(9999) = %v, want %v", got, want)
	}
}
