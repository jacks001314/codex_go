package codemode

import (
	"context"
	"testing"
	"time"
)

// Mirrors Rust #55653's buffered_completion on the session-runtime path: a wait
// that witnesses the completion itself reports a zero buffer.
func TestSessionRuntimeWaitReportsZeroBufferForALiveCompletionLikeRust(t *testing.T) {
	engine := newBlockingEngine()
	runtime := newPreemptRuntime(engine)
	started, err := runtime.Execute(context.Background(), &ExecuteRequest{
		ToolCallID: "live-buffer", Source: "text('released')", YieldTimeMS: uint64Ptr(0),
	}, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(engine.release)
	}()
	wait, err := runtime.Wait(context.Background(), &WaitRequest{CellID: started.CellID, YieldTimeMS: 2000}, nil)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if wait.Response.Variant != "Result" {
		t.Fatalf("wait response = %#v", wait.Response)
	}
	if wait.Response.ResultBufferedForNS != 0 {
		t.Fatalf("live completion buffered for %d ns, want 0", wait.Response.ResultBufferedForNS)
	}
}
