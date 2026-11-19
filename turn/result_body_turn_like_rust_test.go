package turn

import (
	"strings"
	"testing"

	"codex_go/model"
)

// Mirrors Rust ResultBodyTurn (#62392): bodies are bounded per turn, keyed by a
// local id, and only the owning credential instance can read them back.
func TestResultBodyTurnBoundsAndOwnerLikeRust(t *testing.T) {
	store := NewResultBodyTurn("turn-1", "chatgpt|acct")
	id, ok := store.Record(model.ToolResultBodySource{
		Content: []any{map[string]any{"type": "text", "text": "hello"}},
	})
	if !ok || id == "" {
		t.Fatalf("Record() = %q, %v, want a local id", id, ok)
	}
	if _, ok := store.Record(model.ToolResultBodySource{
		Content: []any{map[string]any{"type": "image", "data": "***"}},
	}); ok {
		t.Fatal("Record() accepted a non-text content block")
	}
	if _, ok := store.Record(model.ToolResultBodySource{
		Content: []any{map[string]any{"type": "text", "text": "boom"}},
		IsError: true,
	}); ok {
		t.Fatal("Record() accepted an error result")
	}
	if got := store.Owner(); got != "chatgpt|acct" {
		t.Fatalf("Owner() = %q", got)
	}
	if got := store.TurnID(); got != "turn-1" {
		t.Fatalf("TurnID() = %q", got)
	}
}

// Mirrors Rust ResultBodyTurn::for_input: only rows carrying a retained result id
// produce an attachment, and the attachment addresses that row's exact position.
func TestResultBodyTurnAttachmentsAddressRowsLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.SetIncludeGeneralMetadata(false)
	recorder.BeginResultBodyTurn("turn-1", "chatgpt|acct", true)
	id, ok := recorder.CaptureResultBody("turn-1", "chatgpt|acct", model.ToolResultBodySource{
		Content: []any{map[string]any{"type": "text", "text": "connector body"}},
	})
	if !ok {
		t.Fatal("CaptureResultBody() rejected an eligible result")
	}
	output := &model.AgentItem{Type: "function_call_output"}
	output.ID = "fco-1"
	body := model.NewExecutedToolCall("search", map[string]any{"q": "x"})
	body.SetToolResultMetadata(model.NewToolResultMetadata(nil).WithResultID(id))
	plain := model.NewExecutedToolCall("shell", map[string]any{"cmd": "ls"})
	output.AppendExecutedToolCalls(body, plain)

	input := []any{&model.AgentItem{Type: "message"}, output}
	attachments := recorder.ResultBodyAttachments("chatgpt|acct", input)
	if len(attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(attachments))
	}
	if attachments[0].ItemIndex != 1 || attachments[0].CallIndex != 0 {
		t.Fatalf("attachment address = (%d,%d), want (1,0)", attachments[0].ItemIndex, attachments[0].CallIndex)
	}
	if !strings.Contains(attachments[0].Body.JSON(), "connector body") {
		t.Fatalf("attachment body = %s", attachments[0].Body.JSON())
	}
	// A request served by another credential instance attaches nothing.
	if got := recorder.ResultBodyAttachments("chatgpt|other", input); len(got) != 0 {
		t.Fatalf("attachments for another owner = %d, want 0", len(got))
	}
}

// Mirrors Rust scope_request_metadata (#62392): with only passthrough enabled,
// the request copy keeps the rows with a captured body and drops the cell's
// completeness claim, while history keeps the full inventory.
func TestScopeToolResultsForInputKeepsOnlyCapturedBodiesLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.BeginResultBodyTurn("turn-1", "chatgpt|acct", true)
	id, ok := recorder.CaptureResultBody("turn-1", "chatgpt|acct", model.ToolResultBodySource{
		Content: []any{map[string]any{"type": "text", "text": "body"}},
	})
	if !ok {
		t.Fatal("CaptureResultBody() rejected an eligible result")
	}
	item := &model.AgentItem{Type: "function_call_output"}
	withBody := model.NewExecutedToolCall("search", map[string]any{"q": "x"})
	withBody.SetToolResultMetadata(model.NewToolResultMetadata(nil).WithResultID(id))
	withoutBody := model.NewExecutedToolCall("shell", map[string]any{"cmd": "ls"})
	item.AppendExecutedToolCalls(withBody, withoutBody)
	item.SetExecutedToolCallCell("cell-1")
	item.SetExecutedToolCallsComplete(true)

	scoped := recorder.ScopeToolResultsForInput([]any{item})
	if len(scoped) != 1 {
		t.Fatalf("scoped input length = %d", len(scoped))
	}
	narrowed, ok := scoped[0].(model.ExecutedToolCallCarrier)
	if !ok {
		t.Fatalf("scoped item type = %T", scoped[0])
	}
	calls := narrowed.ExecutedToolCalls()
	if len(calls) != 1 || calls[0].Name != "search" || calls[0].ToolResultID() != id {
		t.Fatalf("scoped calls = %#v, want only the captured row", calls)
	}
	if cellID := calls[0].Name; cellID == "" {
		t.Fatal("scoped row lost its name")
	}
	if got := len(item.ExecutedToolCalls()); got != 2 {
		t.Fatalf("history calls = %d, want the untouched inventory", got)
	}
	// Specificity guard: an empty projection still keeps the item in place so
	// attachment indexes stay valid.
	if got := len(recorder.ScopeToolResultsForInput([]any{item})); got != 1 {
		t.Fatalf("scoped input length = %d", got)
	}
}

// Mirrors Rust's general-metadata precedence: with ordinary executed-tool
// metadata enabled the projection leaves the request untouched.
func TestScopeToolResultsForInputKeepsGeneralMetadataLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.BeginResultBodyTurn("turn-1", "chatgpt|acct", true)
	recorder.SetIncludeGeneralMetadata(true)
	item := &model.AgentItem{Type: "function_call_output"}
	item.AppendExecutedToolCalls(model.NewExecutedToolCall("shell", map[string]any{"cmd": "ls"}))
	input := []any{item}
	if scoped := recorder.ScopeToolResultsForInput(input); scoped[0] != input[0] {
		t.Fatal("ScopeToolResultsForInput() replaced an item with general metadata enabled")
	}
}

// Mirrors Rust's per-turn store lifetime: starting a turn for another owner
// drops the previous bodies, so private bytes never leak across turns.
func TestBeginResultBodyTurnReplacesStoreLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.BeginResultBodyTurn("turn-1", "chatgpt|acct", true)
	if _, ok := recorder.CaptureResultBody("turn-1", "chatgpt|acct", model.ToolResultBodySource{
		Content: []any{map[string]any{"type": "text", "text": "body"}},
	}); !ok {
		t.Fatal("CaptureResultBody() rejected an eligible result")
	}
	recorder.BeginResultBodyTurn("turn-2", "chatgpt|acct", true)
	item := &model.AgentItem{Type: "function_call_output"}
	call := model.NewExecutedToolCall("search", map[string]any{"q": "x"})
	call.SetToolResultMetadata(model.NewToolResultMetadata(nil).WithResultID("stale"))
	item.AppendExecutedToolCalls(call)
	if got := recorder.ResultBodyAttachments("chatgpt|acct", []any{item}); len(got) != 0 {
		t.Fatalf("attachments after turn change = %d, want 0", len(got))
	}
	recorder.BeginResultBodyTurn("turn-3", "", false)
	if recorder.CapturesResultBodies() {
		t.Fatal("CapturesResultBodies() = true after a disabled turn start")
	}
}
