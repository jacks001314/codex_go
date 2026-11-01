package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Mirrors Rust core `handle_runtime_response` (core/src/tools/code_mode/mod.rs):
// a client-truncated session appends its own `Script error:` block and truncates,
// while a server-truncated host already delivered the labeled, budgeted
// observation (#58125) and the client must neither append nor truncate again.

func TestServerTruncatedRemoteFailureKeepsTheHostObservationLikeRust(t *testing.T) {
	hostText := "Warning: truncated output (original token count: 24)\nTotal output lines: 2\n\nScript error:\n…17 tokens truncated…67890123456789"
	response := CodeModeRemoteResponse{
		CellID:       "cell-1",
		State:        "completed",
		ContentItems: []map[string]any{{"type": "input_text", "text": hostText}},
		ErrorText:    strings.Repeat("0123456789", 8),
	}
	output, err := remoteResponseOutput("call", response, 1, CodeModeOutputTruncationServer, time.Millisecond, false)
	if err != nil {
		t.Fatalf("remoteResponseOutput() error = %v", err)
	}
	if output.Success {
		t.Fatalf("server-truncated failure reported success")
	}
	if strings.Count(output.Body, "Script error:") != 1 {
		t.Fatalf("client appended a second error block: %q", output.Body)
	}
	if !strings.HasSuffix(output.Body, hostText) {
		t.Fatalf("client rewrote the host observation: %q", output.Body)
	}
	if strings.Count(output.Body, "Warning: truncated output") != 1 {
		t.Fatalf("client truncated the already-budgeted observation: %q", output.Body)
	}
	items, _ := output.Data["content_items"].([]map[string]any)
	if len(items) != 1 || items[0]["text"] != hostText {
		t.Fatalf("content_items = %#v, want the host's bounded item", items)
	}
}

func TestClientTruncatedRemoteFailureLabelsAndBudgetsOnceLikeRust(t *testing.T) {
	response := CodeModeRemoteResponse{
		CellID:       "cell-1",
		State:        "completed",
		ContentItems: []map[string]any{{"type": "input_text", "text": "partial"}},
		ErrorText:    strings.Repeat("0123456789", 8),
	}
	output, err := remoteResponseOutput("call", response, 1000, CodeModeOutputTruncationClient, time.Millisecond, false)
	if err != nil {
		t.Fatalf("remoteResponseOutput() error = %v", err)
	}
	if output.Success {
		t.Fatalf("client-truncated failure reported success")
	}
	if !strings.Contains(output.Body, "partial\nScript error:\n") {
		t.Fatalf("client did not append exactly one error block after the partial output: %q", output.Body)
	}
	if strings.Count(output.Body, "Script error:") != 1 {
		t.Fatalf("client appended more than one error block: %q", output.Body)
	}
	budgeted, err := remoteResponseOutput("call", response, 7, CodeModeOutputTruncationClient, time.Millisecond, false)
	if err != nil {
		t.Fatalf("remoteResponseOutput() error = %v", err)
	}
	if !strings.Contains(budgeted.Body, "tokens truncated") {
		t.Fatalf("client did not budget the failure: %q", budgeted.Body)
	}
}

// The trailing failure item must survive the cell delta that a yielded cell
// reports on completion, so the host can describe the failure at all.
func TestCellFailureKeepsTheScriptErrorItemLikeRust(t *testing.T) {
	exec, _ := NewCodeModeExecutors(NewRegistry())
	preempt := NewYieldSignal()
	output, err := exec.Execute(context.Background(), &Invocation{
		CallID:  "call-cell-failure",
		Payload: Payload{Kind: PayloadCustom, Input: "text('partial'); throw new Error('boom');"},
		Context: map[string]any{CodeModePreemptContextKey: preempt},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.Success {
		t.Fatalf("failed script reported success")
	}
	if output.Data["error_text"] != "Error: boom" {
		t.Fatalf("error_text = %#v", output.Data["error_text"])
	}
	items, _ := output.Data["content_items"].([]map[string]any)
	if len(items) == 0 {
		t.Fatalf("content_items = %#v", output.Data["content_items"])
	}
	last, _ := items[len(items)-1]["text"].(string)
	if last != "Script error:\nError: boom" {
		t.Fatalf("last item = %q, want the labeled failure", last)
	}
	if !strings.Contains(output.Body, last) {
		t.Fatalf("body = %q, want the failure block", output.Body)
	}
	if output.Data["running"] == true {
		t.Fatalf("failure was reported as a running cell")
	}
}
