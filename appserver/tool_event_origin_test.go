package appserver

import (
	"testing"

	"codex_go/model"
	"codex_go/telemetry"
	"codex_go/turn"
)

// Mirrors Rust #45535's rule that a tool event is classified only from exact
// call-ID evidence for this turn: a call id the model emitted in a sampled
// response is a model tool call, and anything else stays unclassified rather
// than being inferred from cell association or lineage.
func TestToolEventTypeRequiresExactSampledCallEvidenceLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	const threadID = "thread-1"
	const turnID = "turn-1"

	if got := router.toolEventTypeForCall(threadID, turnID, "cmd-1"); got != nil {
		t.Fatalf("classification without evidence = %#v", got)
	}

	router.rememberSampledToolCalls(threadID, turnID, "response-1", []string{"cmd-1", " ", "wait-1"})
	for _, callID := range []string{"cmd-1", "wait-1"} {
		got := router.toolEventTypeForCall(threadID, turnID, callID)
		if got == nil || *got != telemetry.ToolEventTypeModelToolCall {
			t.Fatalf("classification of %s = %#v", callID, got)
		}
	}
	// A call the sampled responses never emitted has no evidence in Go, so the
	// event stays unclassified.
	if got := router.toolEventTypeForCall(threadID, turnID, "child-1"); got != nil {
		t.Fatalf("unrelated call classification = %#v", got)
	}
	// The evidence belongs to one turn.
	if got := router.toolEventTypeForCall(threadID, "turn-2", "cmd-1"); got != nil {
		t.Fatalf("other turn classification = %#v", got)
	}
	// A closed turn drops its evidence (Rust flushes the per-turn state).
	router.forgetSampledToolCalls(threadID, turnID)
	if got := router.toolEventTypeForCall(threadID, turnID, "cmd-1"); got != nil {
		t.Fatalf("classification after turn close = %#v", got)
	}
	// Blank ids are never recorded or classified.
	router.rememberSampledToolCalls(threadID, turnID, "response-1", []string{"  ", ""})
	if got := router.toolEventTypeForCall(threadID, turnID, ""); got != nil {
		t.Fatalf("blank call classification = %#v", got)
	}
}

// Mirrors Rust #45535's inner-call case and its ambiguity rule: a call the cell
// dispatched is an inner tool call, and a call id that is both a sampled model
// call and a cell dispatch stays unclassified.
func TestToolEventTypeClassifiesCodeModeChildCallsLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	const threadID = "thread-1"
	const turnID = "turn-1"
	if err := router.threads.RegisterTurn(threadID, turnID, nil, 1, &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	router.rememberCodeModeChildCall(threadID, "cell-1", "child-1")
	got := router.toolEventTypeForCall(threadID, turnID, "child-1")
	if got == nil || *got != telemetry.ToolEventTypeInnerToolCall {
		t.Fatalf("child call classification = %#v", got)
	}
	// A call id that is also a sampled model call has conflicting evidence.
	router.rememberSampledToolCalls(threadID, turnID, "response-1", []string{"child-1"})
	if got := router.toolEventTypeForCall(threadID, turnID, "child-1"); got != nil {
		t.Fatalf("ambiguous call classification = %#v", got)
	}
	// A child call without an active turn is not attributed to a turn.
	router.rememberCodeModeChildCall("thread-2", "cell-2", "child-2")
	if got := router.toolEventTypeForCall("thread-2", turnID, "child-2"); got != nil {
		t.Fatalf("child call without a turn = %#v", got)
	}
}

// Mirrors the OutputItemDone match Rust collects sampled call ids with.
func TestSampledOutputToolCallIDLikeRust(t *testing.T) {
	cases := []struct {
		item *model.AgentItem
		want string
	}{
		{item: &model.AgentItem{Type: "function_call", CallID: "call-1"}, want: "call-1"},
		{item: &model.AgentItem{Type: "custom_tool_call", CallID: "call-2"}, want: "call-2"},
		{item: &model.AgentItem{Type: "tool_search_call", CallID: "call-3"}, want: "call-3"},
		{item: &model.AgentItem{Type: "local_shell_call", ID: "call-4"}, want: "call-4"},
		{item: &model.AgentItem{Type: "web_search_call", ID: "ws-1"}, want: "ws-1"},
		{item: &model.AgentItem{Type: "image_generation_call", ID: "ig-1"}, want: "ig-1"},
		{item: &model.AgentItem{Type: "message", ID: "msg-1"}, want: ""},
		{item: &model.AgentItem{Type: "reasoning", ID: "reason-1"}, want: ""},
		{item: &model.AgentItem{Type: "function_call"}, want: ""},
		{item: nil, want: ""},
	}
	for _, testCase := range cases {
		if got := sampledOutputToolCallID(testCase.item); got != testCase.want {
			t.Fatalf("sampledOutputToolCallID(%#v) = %q, want %q", testCase.item, got, testCase.want)
		}
	}
}
