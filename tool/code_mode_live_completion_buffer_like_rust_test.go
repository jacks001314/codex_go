package tool

import (
	"context"
	"testing"
)

// Mirrors Rust #55653's buffered_completion: a completion that the observation
// witnessed itself reports a zero buffer, while a result that had already been
// buffered when the observation arrived reports how long it waited.
func TestCodeModeLiveCompletionReportsZeroBufferLikeRust(t *testing.T) {
	exec, _ := NewCodeModeExecutors(NewRegistry())
	output, err := exec.Execute(context.Background(), &Invocation{
		CallID: "live-exec",
		Payload: Payload{Kind: PayloadCustom, Input: `// @exec: {"yield_time_ms": 2000}
			await new Promise(resolve => setTimeout(resolve, 30)); text("done");`},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	buffered, ok := output.Data["result_buffered_for_ns"].(uint64)
	if !ok {
		t.Fatalf("exec carried no result_buffered_for_ns: %#v", output.Data)
	}
	if buffered != 0 {
		t.Fatalf("live exec completion buffered for %d ns, want 0", buffered)
	}
}

func TestCodeModeWaitReportsZeroBufferForALiveCompletionLikeRust(t *testing.T) {
	exec, wait := NewCodeModeExecutors(NewRegistry())
	started, err := exec.Execute(context.Background(), &Invocation{
		CallID: "live-wait-exec",
		Payload: Payload{Kind: PayloadCustom, Input: `// @exec: {"yield_time_ms": 0}
			await new Promise(resolve => setTimeout(resolve, 200)); text("done");`},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	cellID, _ := started.Data["cell_id"].(string)
	if cellID == "" {
		t.Fatalf("exec did not yield a cell: %#v", started.Data)
	}
	completed, err := wait.Execute(context.Background(), &Invocation{
		CallID:  "live-wait",
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"` + cellID + `","yield_time_ms":2000}`},
	})
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if completed.Data["running"] == true {
		t.Fatalf("wait yielded instead of observing the completion: %#v", completed.Data)
	}
	buffered, ok := completed.Data["result_buffered_for_ns"].(uint64)
	if !ok {
		t.Fatalf("wait carried no result_buffered_for_ns: %#v", completed.Data)
	}
	if buffered != 0 {
		t.Fatalf("live wait completion buffered for %d ns, want 0", buffered)
	}
}
