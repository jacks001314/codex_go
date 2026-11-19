package turn

import (
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// Mirrors Rust #62423: an eligible call made inside a Code Mode cell retains its
// result body paired with the child call ID, bound to the originating exec/wait
// output, and the pair reaches the wire together.

func nestedBodySource(text string) model.ToolResultBodySource {
	return model.ToolResultBodySource{Content: []any{map[string]any{"type": "text", "text": text}}}
}

// nestedBodyRecorder builds a recorder that captures bodies for one turn, with a
// started cell whose exec output is already registered.
func nestedBodyRecorder(t *testing.T, cellID string, outputCallID string) *ExecutedToolCallRecorder {
	t.Helper()
	recorder := NewExecutedToolCallRecorder()
	recorder.SetIncludeGeneralMetadata(false)
	recorder.SetResultBodyPassthrough(true)
	recorder.BeginResultBodyTurn("turn-1", "chatgpt|acct", true)
	recorder.StartCell(cellID, outputCallID)
	return recorder
}

// attachNestedCall records one nested call, captures and binds its body, then
// attaches the pending inventory to the exec output. It reports the body ID.
func attachNestedCall(t *testing.T, recorder *ExecutedToolCallRecorder, cellID string, outputCallID string, callID string, text string) string {
	t.Helper()
	recorder.recordNested("cell:"+cellID, callID, model.NewExecutedToolCall("mcp__echo", map[string]any{"q": callID}), 0)
	bodyID, ok := recorder.CaptureNestedResultBody("turn-1", "chatgpt|acct", nestedBodySource(text), callID)
	if !ok {
		t.Fatalf("CaptureNestedResultBody(%q) rejected an eligible result", callID)
	}
	if !recorder.BindToolResultMetadata(lateResultInvocation(cellID, callID), model.NewToolResultMetadata(nil).WithResultID(bodyID)) {
		t.Fatalf("BindToolResultMetadata(%q) dropped a body-only observation", callID)
	}
	history := []any{codeModeExecInputItem(outputCallID), codeModeExecOutputItem(outputCallID)}
	attached, token := recorder.AttachPendingToPrompt(history)
	if token == nil {
		t.Fatal("the nested inventory must attach to the exec output")
	}
	recorder.CommitAttachment(token)
	calls := model.RequestInputExecutedToolCalls(attached[1])
	if len(calls) != 1 || calls[0].ToolResultID() != bodyID {
		t.Fatalf("attached output rows = %#v, want one row bound to the captured body", calls)
	}
	return bodyID
}

// Mirrors Rust ResultBodyTurn::record with a nested call ID: the captured body
// remembers the child call it names so a request emits both fields.
func TestCaptureNestedResultBodyPairsCallIDLikeRust(t *testing.T) {
	recorder := nestedBodyRecorder(t, "pair-cell", "pair-exec")
	recorder.recordNested("cell:pair-cell", "child-7", model.NewExecutedToolCall("mcp__echo", map[string]any{"q": "x"}), 0)
	bodyID, ok := recorder.CaptureNestedResultBody("turn-1", "chatgpt|acct", nestedBodySource("nested body"), "child-7")
	if !ok || bodyID == "" {
		t.Fatalf("CaptureNestedResultBody() = (%q, %v), want a local id", bodyID, ok)
	}
	if !recorder.BindToolResultMetadata(lateResultInvocation("pair-cell", "child-7"), model.NewToolResultMetadata(nil).WithResultID(bodyID)) {
		t.Fatal("BindToolResultMetadata() dropped the body-only observation")
	}
	history := []any{codeModeExecInputItem("pair-exec"), codeModeExecOutputItem("pair-exec")}
	attached, token := recorder.AttachPendingToPrompt(history)
	if token == nil {
		t.Fatal("the nested inventory must attach to the exec output")
	}
	attachments := recorder.ResultBodyAttachments("chatgpt|acct", attached)
	if len(attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(attachments))
	}
	if got := attachments[0].Body.NestedCallIDJSON(); got != `"child-7"` {
		t.Fatalf("NestedCallIDJSON() = %s, want %q", got, `"child-7"`)
	}
	// The attachments address the attached slice, so the encoded request keeps
	// the same shape: one placeholder item ahead of the output.
	wire, ok := model.WithToolResults(`{"input":[{},{"internal_chat_message_metadata_passthrough":{"executed_tool_calls":[{"name":"mcp__echo","arguments":{"q":"x"}}]}}]}`, attachments)
	if !ok {
		t.Fatal("WithToolResults reported no enrichment")
	}
	if !strings.Contains(wire, `"tool_result_call_id":"child-7"`) {
		t.Fatalf("wire request lost the child call ID: %s", wire)
	}
	if !strings.Contains(wire, "nested body") {
		t.Fatalf("wire request lost the nested body: %s", wire)
	}
	if other := recorder.ResultBodyAttachments("chatgpt|other", attached); len(other) != 0 {
		t.Fatalf("attachments for another owner = %d, want 0", len(other))
	}
}

// Mirrors Rust #62423 reclaiming capture capacity: a body is released with the
// output binding that justified it, so the bytes are available again and the row
// stops carrying the local ID.
func TestNestedResultBodyIsReclaimedWithItsOutputLikeRust(t *testing.T) {
	recorder := nestedBodyRecorder(t, "reclaim-cell", "reclaim-exec")
	bodyID := attachNestedCall(t, recorder, "reclaim-cell", "reclaim-exec", "child-1", "reclaim me")

	store := recorder.currentResultBodyTurn()
	if store == nil {
		t.Fatal("the turn lost its body store")
	}
	store.mu.Lock()
	before := store.bytes
	store.mu.Unlock()
	if before == 0 {
		t.Fatal("the nested body was never retained")
	}

	binding := recorder.bindingForOutput("reclaim-exec")
	if binding == nil || len(binding.calls) == 0 {
		t.Fatal("the attached output kept no binding")
	}
	if got := binding.calls[0].call.ToolResultID(); got != bodyID {
		t.Fatalf("bound row result id = %q, want %q", got, bodyID)
	}

	// Reusing the runtime handle revokes the cell's output binding, which must
	// also release the private bytes it carried and stop the row addressing them.
	recorder.dropReusedCell("reclaim-cell")
	store.mu.Lock()
	after := store.bytes
	store.mu.Unlock()
	if after != 0 {
		t.Fatalf("retained bytes after reclamation = %d, want 0", after)
	}
	if got := binding.calls[0].call.ToolResultID(); got != "" {
		t.Fatalf("reclaimed row result id = %q, want it cleared", got)
	}
	if _, ok := recorder.CaptureResultBody("turn-1", "chatgpt|acct", nestedBodySource("fresh")); !ok {
		t.Fatal("the reclaimed budget did not admit a later body")
	}
}

// Mirrors Rust #62423: a late nested result whose output is gone keeps no body,
// even though the observation itself is still recorded.
func TestLateNestedResultForDeadOutputKeepsNoBodyLikeRust(t *testing.T) {
	recorder := nestedBodyRecorder(t, "late-cell", "late-exec")
	recorder.recordNested("cell:late-cell", "child-9", model.NewExecutedToolCall("mcp__echo", map[string]any{"q": "x"}), 0)
	history := []any{codeModeExecInputItem("late-exec"), codeModeExecOutputItem("late-exec")}
	if _, token := recorder.AttachPendingToPrompt(history); token == nil {
		t.Fatal("the nested inventory must attach to the exec output")
	} else {
		recorder.CommitAttachment(token)
	}
	recorder.dropReusedCell("late-cell")

	bodyID, ok := recorder.CaptureResultBody("turn-1", "chatgpt|acct", nestedBodySource("late body"))
	if !ok {
		t.Fatal("CaptureResultBody() rejected an eligible result")
	}
	if !recorder.BindToolResultMetadata(lateResultInvocation("late-cell", "child-9"), model.NewToolResultMetadata(nil).WithResultID(bodyID)) {
		t.Fatal("the late observation was dropped instead of being recorded")
	}
	if attachments := recorder.ResultBodyAttachments("chatgpt|acct", history); len(attachments) != 0 {
		t.Fatalf("attachments for a dead output = %d, want 0", len(attachments))
	}
	store := recorder.currentResultBodyTurn()
	store.mu.Lock()
	retainedBytes := store.bytes
	store.mu.Unlock()
	if retainedBytes != 0 {
		t.Fatalf("retained bytes for a dead output = %d, want 0", retainedBytes)
	}
}

// Mirrors Rust #62423: a cell that lost its output binding cannot send bodies
// with the attachment it produces.
func TestInvalidCellBindingSendsNoBodiesLikeRust(t *testing.T) {
	recorder := nestedBodyRecorder(t, "invalid-cell", "invalid-exec")
	recorder.recordNested("cell:invalid-cell", "child-5", model.NewExecutedToolCall("mcp__echo", map[string]any{"q": "x"}), 0)
	bodyID, ok := recorder.CaptureNestedResultBody("turn-1", "chatgpt|acct", nestedBodySource("unbound body"), "child-5")
	if !ok {
		t.Fatalf("CaptureNestedResultBody() = %v, want a local id", ok)
	}
	if !recorder.BindToolResultMetadata(lateResultInvocation("invalid-cell", "child-5"), model.NewToolResultMetadata(nil).WithResultID(bodyID)) {
		t.Fatal("BindToolResultMetadata() dropped a body-only observation")
	}
	// A duplicated output makes the cell's attribution ambiguous.
	history := []any{codeModeExecInputItem("invalid-exec"), codeModeExecOutputItem("invalid-exec"), codeModeExecOutputItem("invalid-exec")}
	attached, _ := recorder.AttachPendingToPrompt(history)
	if attachments := recorder.ResultBodyAttachments("chatgpt|acct", attached); len(attachments) != 0 {
		t.Fatalf("attachments from an ambiguous output = %d, want 0", len(attachments))
	}
}

var _ = tool.Invocation{}
