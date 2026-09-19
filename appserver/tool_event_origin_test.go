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
	// A child call is only evidence while its cell is known (Rust records a
	// child call only for a cell it has seen start).
	router.rememberCodeModeChildCall(threadID, turnID, "cell-1", "child-1")
	if got := router.toolEventTypeForCall(threadID, turnID, "child-1"); got != nil {
		t.Fatalf("child call without a known cell = %#v", got)
	}
	router.rememberCodeModeCell(threadID, turnID, "cell-1", "exec-1")
	router.rememberCodeModeChildCall(threadID, turnID, "cell-1", "child-1")
	got := router.toolEventTypeForCall(threadID, turnID, "child-1")
	if got == nil || *got != telemetry.ToolEventTypeInnerToolCall {
		t.Fatalf("child call classification = %#v", got)
	}
	// A call id that is also a sampled model call has conflicting evidence.
	router.rememberSampledToolCalls(threadID, turnID, "response-1", []string{"child-1"})
	if got := router.toolEventTypeForCall(threadID, turnID, "child-1"); got != nil {
		t.Fatalf("ambiguous call classification = %#v", got)
	}
	// A child call without a turn id is not attributed to any turn.
	router.rememberCodeModeCell("thread-2", turnID, "cell-2", "exec-2")
	router.rememberCodeModeChildCall("thread-2", "", "cell-2", "child-2")
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

// Mirrors Rust #36729's `enrich_tool_response_event`: a child call takes its cell
// and parent call from the cell evidence, its originating response is its own
// sampled response or the cell's, and a child call whose cell is unknown loses
// both correlation fields.
func TestEnrichToolEventBaseCorrelatesCodeModeCallsLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	const threadID = "thread-1"
	const turnID = "turn-1"

	// The exec call was sampled in response-1 and started cell-1.
	router.rememberSampledToolCalls(threadID, turnID, "response-1", []string{"exec-1"})
	router.rememberCodeModeCell(threadID, turnID, "cell-1", "exec-1")
	// The child call was dispatched inside the cell and answered by response-2.
	router.rememberCodeModeChildCall(threadID, turnID, "cell-1", "child-1")
	router.rememberSampledToolCalls(threadID, turnID, "response-2", []string{"child-1"})

	child := telemetry.CodexToolItemEventBase{ItemID: "child-1"}
	router.enrichToolEventBase(&child, threadID, turnID)
	if child.CellID == nil || *child.CellID != "cell-1" {
		t.Fatalf("child cell = %#v", child.CellID)
	}
	if child.ParentCallID == nil || *child.ParentCallID != "exec-1" {
		t.Fatalf("child parent call = %#v", child.ParentCallID)
	}
	if child.OriginatingResponseID == nil || *child.OriginatingResponseID != "response-2" {
		t.Fatalf("child originating response = %#v", child.OriginatingResponseID)
	}
	if child.SubsequentResponseID != nil {
		t.Fatalf("subsequent response must stay absent: %#v", child.SubsequentResponseID)
	}
	if child.SessionID != threadID {
		t.Fatalf("session id = %q", child.SessionID)
	}

	// A child call without its own sampled response falls back to the cell's.
	router.rememberCodeModeChildCall(threadID, turnID, "cell-1", "child-2")
	withoutOwnResponse := telemetry.CodexToolItemEventBase{ItemID: "child-2"}
	router.enrichToolEventBase(&withoutOwnResponse, threadID, turnID)
	if withoutOwnResponse.OriginatingResponseID == nil || *withoutOwnResponse.OriginatingResponseID != "response-1" {
		t.Fatalf("cell originating response = %#v", withoutOwnResponse.OriginatingResponseID)
	}
	if withoutOwnResponse.ParentCallID == nil || *withoutOwnResponse.ParentCallID != "exec-1" {
		t.Fatalf("cell fallback parent call = %#v", withoutOwnResponse.ParentCallID)
	}

	// The cell's own call is not a child call, so it correlates only by response.
	execCall := telemetry.CodexToolItemEventBase{ItemID: "exec-1"}
	router.enrichToolEventBase(&execCall, threadID, turnID)
	if execCall.CellID != nil || execCall.ParentCallID != nil {
		t.Fatalf("exec call correlation = %#v/%#v", execCall.CellID, execCall.ParentCallID)
	}
	if execCall.OriginatingResponseID == nil || *execCall.OriginatingResponseID != "response-1" {
		t.Fatalf("exec originating response = %#v", execCall.OriginatingResponseID)
	}

	// A child call whose cell is unknown keeps neither field.
	router.rememberCodeModeChildCall(threadID, turnID, "cell-unknown", "child-3")
	orphan := telemetry.CodexToolItemEventBase{ItemID: "child-3"}
	router.enrichToolEventBase(&orphan, threadID, turnID)
	if orphan.CellID != nil || orphan.ParentCallID != nil {
		t.Fatalf("orphan child correlation = %#v/%#v", orphan.CellID, orphan.ParentCallID)
	}

	// Closing the turn drops the closed cell and the turn's child evidence.
	router.closeCodeModeCell(threadID, turnID, "cell-1")
	router.forgetSampledToolCalls(threadID, turnID)
	closed := telemetry.CodexToolItemEventBase{ItemID: "child-1"}
	router.enrichToolEventBase(&closed, threadID, turnID)
	if closed.CellID != nil || closed.ParentCallID != nil || closed.OriginatingResponseID != nil {
		t.Fatalf("correlation after turn close = %#v", closed)
	}
}
