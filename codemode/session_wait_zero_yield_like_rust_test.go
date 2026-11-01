package codemode

import (
	"context"
	"testing"
	"time"
)

// Mirrors Rust's WaitRequest.yield_time_ms: the protocol field is a plain u64
// with no default, so a zero observes the cell immediately. Only the tool
// argument layer (ExecWaitArgs) supplies the 10 000 ms default.
func TestSessionRuntimeWaitTreatsAnExplicitZeroYieldAsImmediateLikeRust(t *testing.T) {
	engine := newBlockingEngine()
	runtime := newPreemptRuntime(engine)
	started, err := runtime.Execute(context.Background(), &ExecuteRequest{
		ToolCallID: "call-zero-wait", Source: "text('released')", YieldTimeMS: uint64Ptr(0),
	}, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if started.InitialResponse.Variant != "Yielded" {
		t.Fatalf("zero-yield execute response = %#v", started.InitialResponse)
	}
	began := time.Now()
	wait, err := runtime.Wait(context.Background(), &WaitRequest{CellID: started.CellID, YieldTimeMS: 0}, nil)
	elapsed := time.Since(began)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("zero-yield wait blocked for %v", elapsed)
	}
	if wait.Response.Variant != "Yielded" {
		t.Fatalf("zero-yield wait response = %#v", wait.Response)
	}
	// A released script still completes normally once the caller waits for it.
	close(engine.release)
	completed, err := runtime.Wait(context.Background(), &WaitRequest{CellID: started.CellID, YieldTimeMS: 1000}, nil)
	if err != nil {
		t.Fatalf("Wait() after release error = %v", err)
	}
	if completed.Response.Variant != "Result" || len(completed.Response.ContentItems) != 1 || completed.Response.ContentItems[0].Text != "released" {
		t.Fatalf("wait after release = %#v", completed.Response)
	}
}
