package codemode

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"codex_go/tool"
)

// Mirrors Rust #55653: the stdio wire carries `result_buffered_for_ns` for a
// Result, defaults it to zero when an older host omits it, and never attaches
// it to Yielded or Terminated.
func TestRuntimeResponseBufferedResultWireLikeRust(t *testing.T) {
	result := Result(NewCellID("cell-1"), []ContentItem{InputText("done")}, nil)
	result.ResultBufferedForNS = 1234
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var envelope map[string]map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if got := string(envelope["Result"]["result_buffered_for_ns"]); got != "1234" {
		t.Fatalf("wire result_buffered_for_ns = %s, want 1234", got)
	}

	for _, response := range []RuntimeResponse{
		Yielded(NewCellID("cell-1"), nil),
		Terminated(NewCellID("cell-1"), nil),
	} {
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatalf("marshal %s: %v", response.Variant, err)
		}
		var envelope map[string]map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &envelope); err != nil {
			t.Fatalf("decode %s: %v", response.Variant, err)
		}
		for variant, body := range envelope {
			if _, ok := body["result_buffered_for_ns"]; ok {
				t.Fatalf("%s carried result_buffered_for_ns", variant)
			}
		}
	}

	// Older hosts omit the field, which must decode as an immediate delivery.
	var legacy RuntimeResponse
	if err := json.Unmarshal([]byte(`{"Result":{"cell_id":"1","content_items":[],"error_text":null,"code_mode_host_duration_ns":7}}`), &legacy); err != nil {
		t.Fatalf("decode legacy result: %v", err)
	}
	if legacy.Variant != "Result" || legacy.ResultBufferedForNS != 0 || legacy.CodeModeHostDurationNS != 7 {
		t.Fatalf("legacy result = %#v", legacy)
	}

	var buffered RuntimeResponse
	if err := json.Unmarshal([]byte(`{"Result":{"cell_id":"1","content_items":[],"error_text":null,"result_buffered_for_ns":42,"code_mode_host_duration_ns":7}}`), &buffered); err != nil {
		t.Fatalf("decode buffered result: %v", err)
	}
	if buffered.ResultBufferedForNS != 42 {
		t.Fatalf("buffered result = %#v", buffered)
	}
}

// Mirrors Rust's `WaitOutcome::result_buffered_for`: only a LiveCell Result has a
// measurement, and zero is still a measurement (immediate delivery).
func TestWaitOutcomeResultBufferedForLikeRust(t *testing.T) {
	completed := LiveCell(Result(NewCellID("1"), nil, nil))
	completed.Response.ResultBufferedForNS = 987
	if buffered, ok := completed.ResultBufferedFor(); !ok || buffered != 987*time.Nanosecond {
		t.Fatalf("buffered = %v, %v", buffered, ok)
	}
	immediate := LiveCell(Result(NewCellID("1"), nil, nil))
	if buffered, ok := immediate.ResultBufferedFor(); !ok || buffered != 0 {
		t.Fatalf("immediate = %v, %v", buffered, ok)
	}
	for name, outcome := range map[string]WaitOutcome{
		"yielded":    LiveCell(Yielded(NewCellID("1"), nil)),
		"terminated": LiveCell(Terminated(NewCellID("1"), nil)),
		"missing":    MissingCell(Result(NewCellID("1"), nil, nil)),
	} {
		if buffered, ok := outcome.ResultBufferedFor(); ok || buffered != 0 {
			t.Fatalf("%s buffered = %v, %v", name, buffered, ok)
		}
	}
	var absent *WaitOutcome
	if buffered, ok := absent.ResultBufferedFor(); ok || buffered != 0 {
		t.Fatalf("absent buffered = %v, %v", buffered, ok)
	}
}

// Mirrors Rust's paused-clock buffering test: an immediate Result reports zero,
// while a Result retrieved later reports the time it stayed buffered.
func TestSessionRuntimeBufferedResultLikeRust(t *testing.T) {
	runtime := NewSessionRuntime()
	now := time.Unix(1_700_000_000, 0)
	runtime.Cells().SetClock(func() time.Time { return now })

	yield := uint64(0)
	started, err := runtime.Execute(context.Background(), &ExecuteRequest{
		ToolCallID:  "call-1",
		Source:      `text("done")`,
		YieldTimeMS: &yield,
	}, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if started.InitialResponse.Variant != "Yielded" {
		t.Fatalf("initial = %#v", started.InitialResponse)
	}
	waitForCellStatus(t, runtime.Cells(), started.CellID.String(), CellCompleted, 2*time.Second)

	now = now.Add(30 * time.Second)
	outcome, err := runtime.Wait(context.Background(), &WaitRequest{CellID: started.CellID, YieldTimeMS: 1000}, nil)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if outcome.Response.Variant != "Result" {
		t.Fatalf("wait outcome = %#v", outcome.Response)
	}
	if buffered, ok := outcome.ResultBufferedFor(); !ok || buffered != 30*time.Second {
		t.Fatalf("wait buffered = %v, %v", buffered, ok)
	}

	// A termination that claims the same completed result keeps it a Result and
	// measures the buffering again at retrieval time.
	now = now.Add(5 * time.Second)
	terminated, err := runtime.Terminate(TerminateRequestForCell(started.CellID))
	if err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if terminated.Response.Variant != "Result" {
		t.Fatalf("terminate outcome = %#v", terminated.Response)
	}
	if buffered, ok := terminated.ResultBufferedFor(); !ok || buffered != 35*time.Second {
		t.Fatalf("terminate buffered = %v, %v", buffered, ok)
	}
}

// Immediate delivery from the same Execute call reports zero buffering.
func TestSessionRuntimeImmediateResultHasNoBufferingLikeRust(t *testing.T) {
	runtime := NewSessionRuntime()
	started, err := runtime.Execute(context.Background(), &ExecuteRequest{
		ToolCallID: "call-1",
		Source:     `text("done")`,
	}, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if started.InitialResponse.Variant != "Result" {
		t.Fatalf("initial = %#v", started.InitialResponse)
	}
	outcome := LiveCell(started.InitialResponse)
	if buffered, ok := outcome.ResultBufferedFor(); !ok || buffered != 0 {
		t.Fatalf("buffered = %v, %v", buffered, ok)
	}
}

// The in-process stdio host copies the wait handler's buffering measurement onto
// the wire response.
func TestHostRuntimeResponseCarriesBufferedResultLikeRust(t *testing.T) {
	response := hostRuntimeResponse(NewCellID("cell-1"), &tool.Output{Data: map[string]any{
		"result_buffered_for_ns": uint64(555),
	}}, nil)
	if response.Variant != "Result" || response.ResultBufferedForNS != 555 {
		t.Fatalf("response = %#v", response)
	}
}

func waitForCellStatus(t *testing.T, cells *CellStore, cellID string, status CellStatus, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cell, ok := cells.Get(cellID); ok && cell.Status == status {
			return
		}
		if time.Now().After(deadline) {
			cell, _ := cells.Get(cellID)
			t.Fatalf("cell %s never reached %s: %#v", cellID, status, cell)
		}
		time.Sleep(time.Millisecond)
	}
}
