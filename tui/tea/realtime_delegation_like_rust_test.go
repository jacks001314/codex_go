package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/utils"
)

const realtimeDelegationTestPrompt = "<realtime_delegation><input>inspect the workspace</input></realtime_delegation>"

func newRealtimeDelegationTestModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-voice")
	model := NewModel(state, Options{Width: 100, Height: 40})
	model.ensureSize()
	return model
}

func feedThreadEvents(model *Model, events ...protocol.ThreadEvent) {
	for _, event := range events {
		model.Update(ThreadEventMsg{Event: event})
	}
}

func delegationItem(id string) *protocol.ThreadItem {
	return &protocol.ThreadItem{ID: id, Type: "user_message", Text: realtimeDelegationTestPrompt}
}

// TestDelegatedTurnHidesPrivateContentLikeRust feeds a real protocol event
// sequence through Model.Update and checks the rendered output: a voice
// handoff turn keeps its final answer but never paints the delegated
// reasoning or commentary (Rust is_private_realtime_agent_item), and the turn
// marker survives turn completion for replay.
func TestDelegatedTurnHidesPrivateContentLikeRust(t *testing.T) {
	model := newRealtimeDelegationTestModel(t)
	feedThreadEvents(model,
		protocol.ThreadEvent{Type: "turn.started", ThreadID: "thread-voice", TurnID: "turn-voice"},
		protocol.ThreadEvent{Type: "item.started", ThreadID: "thread-voice", TurnID: "turn-voice", Item: delegationItem("user-1")},
		protocol.ThreadEvent{Type: "item.completed", ThreadID: "thread-voice", TurnID: "turn-voice", Item: delegationItem("user-1")},
		protocol.ThreadEvent{Type: "item.reasoning.delta", ThreadID: "thread-voice", TurnID: "turn-voice", Delta: &protocol.Delta{ItemID: "reasoning-1", Text: "private reasoning"}},
		protocol.ThreadEvent{Type: "item.completed", ThreadID: "thread-voice", TurnID: "turn-voice", Item: &protocol.ThreadItem{ID: "reasoning-1", Type: "reasoning", Summary: []string{"private reasoning"}}},
		protocol.ThreadEvent{Type: "item.delta", ThreadID: "thread-voice", TurnID: "turn-voice", Delta: &protocol.Delta{ItemID: "commentary-1", Text: "private commentary"}},
		protocol.ThreadEvent{Type: "item.completed", ThreadID: "thread-voice", TurnID: "turn-voice", Item: &protocol.ThreadItem{ID: "commentary-1", Type: "agent_message", Phase: "commentary", Text: "private commentary"}},
		protocol.ThreadEvent{Type: "item.delta", ThreadID: "thread-voice", TurnID: "turn-voice", Delta: &protocol.Delta{ItemID: "agent-1", Text: "the public answer"}},
		protocol.ThreadEvent{Type: "item.completed", ThreadID: "thread-voice", TurnID: "turn-voice", Item: &protocol.ThreadItem{ID: "agent-1", Type: "agent_message", Phase: "final_answer", Text: "the public answer"}},
		protocol.ThreadEvent{Type: "turn.completed", ThreadID: "thread-voice", TurnID: "turn-voice"},
	)

	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "the public answer") {
		t.Fatalf("final answer missing from the rendered view:\n%s", view)
	}
	if strings.Contains(view, "private commentary") {
		t.Fatalf("delegated commentary leaked into the rendered view:\n%s", view)
	}
	expanded := utils.StripANSI(renderTranscriptWithHistoryMode(model.State, false, 100, "", true))
	if strings.Contains(expanded, "private reasoning") {
		t.Fatalf("delegated reasoning leaked into the expanded transcript:\n%s", expanded)
	}
	if !model.turnIsDelegated("turn-voice") {
		t.Fatal("turn completion dropped the delegated marker; replay would show private content")
	}
}

// TestTypedTurnKeepsReasoningAndCommentaryLikeRust is the control case: without
// a voice handoff nothing is suppressed.
func TestTypedTurnKeepsReasoningAndCommentaryLikeRust(t *testing.T) {
	model := newRealtimeDelegationTestModel(t)
	feedThreadEvents(model,
		protocol.ThreadEvent{Type: "turn.started", ThreadID: "thread-voice", TurnID: "turn-typed"},
		protocol.ThreadEvent{Type: "item.completed", ThreadID: "thread-voice", TurnID: "turn-typed", Item: &protocol.ThreadItem{ID: "user-1", Type: "user_message", Text: "typed question"}},
		protocol.ThreadEvent{Type: "item.reasoning.delta", ThreadID: "thread-voice", TurnID: "turn-typed", Delta: &protocol.Delta{ItemID: "reasoning-1", Text: "typed reasoning"}},
		protocol.ThreadEvent{Type: "item.completed", ThreadID: "thread-voice", TurnID: "turn-typed", Item: &protocol.ThreadItem{ID: "reasoning-1", Type: "reasoning", Summary: []string{"typed reasoning"}}},
		protocol.ThreadEvent{Type: "item.delta", ThreadID: "thread-voice", TurnID: "turn-typed", Delta: &protocol.Delta{ItemID: "commentary-1", Text: "typed commentary"}},
		protocol.ThreadEvent{Type: "item.completed", ThreadID: "thread-voice", TurnID: "turn-typed", Item: &protocol.ThreadItem{ID: "commentary-1", Type: "agent_message", Phase: "commentary", Text: "typed commentary"}},
	)
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "typed commentary") {
		t.Fatalf("typed commentary disappeared:\n%s", view)
	}
	expanded := utils.StripANSI(renderTranscriptWithHistoryMode(model.State, false, 100, "", true))
	if !strings.Contains(expanded, "typed reasoning") {
		t.Fatalf("typed reasoning disappeared:\n%q", expanded)
	}
	if model.turnIsDelegated("turn-typed") {
		t.Fatal("a typed turn was marked delegated")
	}
}

// TestTypedReasoningStartedBeforeHandoffStaysVisibleLikeRust mirrors Rust
// RealtimeAgentItemOrigin::Typed: an item that started while the turn was still
// typed work survives a later voice handoff on the same turn.
func TestTypedReasoningStartedBeforeHandoffStaysVisibleLikeRust(t *testing.T) {
	model := newRealtimeDelegationTestModel(t)
	feedThreadEvents(model,
		protocol.ThreadEvent{Type: "turn.started", ThreadID: "thread-voice", TurnID: "turn-mixed"},
		protocol.ThreadEvent{Type: "item.started", ThreadID: "thread-voice", TurnID: "turn-mixed", Item: &protocol.ThreadItem{ID: "reasoning-typed", Type: "reasoning"}},
		protocol.ThreadEvent{Type: "item.started", ThreadID: "thread-voice", TurnID: "turn-mixed", Item: delegationItem("user-1")},
		protocol.ThreadEvent{Type: "item.reasoning.delta", ThreadID: "thread-voice", TurnID: "turn-mixed", Delta: &protocol.Delta{ItemID: "reasoning-typed", Text: "typed reasoning"}},
		protocol.ThreadEvent{Type: "item.reasoning.delta", ThreadID: "thread-voice", TurnID: "turn-mixed", Delta: &protocol.Delta{ItemID: "reasoning-voice", Text: "private reasoning"}},
		protocol.ThreadEvent{Type: "item.completed", ThreadID: "thread-voice", TurnID: "turn-mixed", Item: &protocol.ThreadItem{ID: "reasoning-typed", Type: "reasoning", Summary: []string{"typed reasoning"}}},
		protocol.ThreadEvent{Type: "item.completed", ThreadID: "thread-voice", TurnID: "turn-mixed", Item: &protocol.ThreadItem{ID: "reasoning-voice", Type: "reasoning", Summary: []string{"private reasoning"}}},
	)
	expanded := utils.StripANSI(renderTranscriptWithHistoryMode(model.State, false, 100, "", true))
	if !strings.Contains(expanded, "typed reasoning") {
		t.Fatalf("pre-handoff typed reasoning disappeared:\n%q", expanded)
	}
	if strings.Contains(expanded, "private reasoning") {
		t.Fatalf("post-handoff reasoning leaked:\n%s", expanded)
	}
}

// TestDelegatedMarkersDoNotCrossThreadsLikeRust pins the thread-scoped reset:
// switching threads must not inherit the previous thread's spoken turns.
func TestDelegatedMarkersDoNotCrossThreadsLikeRust(t *testing.T) {
	model := newRealtimeDelegationTestModel(t)
	feedThreadEvents(model,
		protocol.ThreadEvent{Type: "turn.started", ThreadID: "thread-voice", TurnID: "turn-voice"},
		protocol.ThreadEvent{Type: "item.completed", ThreadID: "thread-voice", TurnID: "turn-voice", Item: delegationItem("user-1")},
	)
	if !model.turnIsDelegated("turn-voice") {
		t.Fatal("delegation prompt did not mark its turn")
	}
	feedThreadEvents(model, protocol.ThreadEvent{Type: "thread.started", ThreadID: "thread-other"})
	if model.turnIsDelegated("turn-voice") {
		t.Fatal("the previous thread's delegated marker crossed into the new thread")
	}
}
