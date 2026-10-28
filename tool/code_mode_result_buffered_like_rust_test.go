package tool

import (
	"context"
	"testing"
	"time"
)

// Mirrors Rust #55653: a wait that retrieves an already-buffered Result reports
// how long it stayed buffered, and the cell keeps its completed (not
// terminated) state when a later termination claims it.
func TestCodeModeWaitReportsBufferedResultLikeRust(t *testing.T) {
	registry := NewRegistry()
	exec, wait := NewCodeModeExecutors(registry)
	started, err := exec.Execute(context.Background(), &Invocation{
		CallID: "buffered",
		Payload: Payload{Kind: PayloadCustom, Input: `// @exec: {"yield_time_ms": 0}
			await new Promise(resolve => setTimeout(resolve, 20)); text("done");`},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	cellID, _ := started.Data["cell_id"].(string)
	if cellID == "" {
		t.Fatalf("started = %#v", started)
	}
	time.Sleep(60 * time.Millisecond)

	completed, err := wait.Execute(context.Background(), &Invocation{
		CallID:  "buffered-wait",
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"` + cellID + `","yield_time_ms":1000}`},
	})
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if completed.Data["terminated"] == true {
		t.Fatalf("completed wait reported termination: %#v", completed.Data)
	}
	buffered, ok := completed.Data["result_buffered_for_ns"].(uint64)
	if !ok || buffered < uint64(20*time.Millisecond) {
		t.Fatalf("result_buffered_for_ns = %#v", completed.Data["result_buffered_for_ns"])
	}
}

// A termination that claims an already-finished cell reports the buffered Result
// instead of turning it into a terminated observation (Rust #55653).
func TestCodeModeTerminateClaimsBufferedResultLikeRust(t *testing.T) {
	registry := NewRegistry()
	exec, wait := NewCodeModeExecutors(registry)
	started, err := exec.Execute(context.Background(), &Invocation{
		CallID: "claim",
		Payload: Payload{Kind: PayloadCustom, Input: `// @exec: {"yield_time_ms": 0}
			text("done");`},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	cellID, _ := started.Data["cell_id"].(string)
	time.Sleep(60 * time.Millisecond)

	terminated, err := wait.Execute(context.Background(), &Invocation{
		CallID:  "claim-terminate",
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"` + cellID + `","terminate":true}`},
	})
	if err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if terminated.Data["terminated"] == true {
		t.Fatalf("terminate reported a terminated observation: %#v", terminated.Data)
	}
	if _, ok := terminated.Data["result_buffered_for_ns"].(uint64); !ok {
		t.Fatalf("terminate dropped result_buffered_for_ns: %#v", terminated.Data)
	}
}

// Only completed remote responses carry the measurement.
func TestRemoteResponseOutputBufferedResultLikeRust(t *testing.T) {
	completed, err := remoteResponseOutput("call", CodeModeRemoteResponse{
		CellID:              "cell-1",
		State:               "completed",
		ContentItems:        []map[string]any{{"type": "input_text", "text": "done"}},
		ResultBufferedForNS: 321,
	}, 1000, time.Millisecond, false)
	if err != nil {
		t.Fatalf("completed: %v", err)
	}
	if got := completed.Data["result_buffered_for_ns"]; got != uint64(321) {
		t.Fatalf("completed result_buffered_for_ns = %#v", got)
	}
	for _, state := range []string{"yielded", "terminated"} {
		output, err := remoteResponseOutput("call", CodeModeRemoteResponse{
			CellID:              "cell-1",
			State:               state,
			ResultBufferedForNS: 321,
		}, 1000, time.Millisecond, false)
		if err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		if _, ok := output.Data["result_buffered_for_ns"]; ok {
			t.Fatalf("%s carried result_buffered_for_ns", state)
		}
	}
}
