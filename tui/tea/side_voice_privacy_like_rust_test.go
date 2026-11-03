package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
)

// Rust parity tests for codex-rs/tui/src/app/thread_events.rs: a side
// conversation is forked into its own thread (app_server_session.rs:1156-1176),
// so it owns a ThreadEventStore of its own. thread_events.rs:408-410 filters
// private items of every delegated turn when the store is snapshotted, and
// thread_events.rs:465-490 drops the same notifications from the replay buffer.
// Go keeps side transcripts as per-thread snapshots, so the same bookkeeping has
// to live on the snapshot.

const sideVoiceDelegationPrompt = "<realtime_delegation><input>book a flight to Osaka</input></realtime_delegation>"

func sideDelegationItem(id string) protocol.ThreadItem {
	return protocol.ThreadItem{ID: id, Type: "user_message", Text: sideVoiceDelegationPrompt}
}

// The parent thread keeps running while the user is in the side conversation.
// A voice handoff in that background turn must be filtered exactly like the
// live transcript: private commentary dropped, final answer kept.
func TestSideParentSnapshotHidesVoiceHandoffOutputLikeRust(t *testing.T) {
	model, _, _ := newSideNavigationModel(t, nil)
	openSideForTest(t, model)

	// The app-server bridge forwards turn/started for inactive threads but the
	// item events it routes to a background thread carry no turn id
	// (app/remote_tui.go:5198, :5258), so the snapshot has to learn the turn
	// from the turn/started notification itself.
	model.Update(ThreadScopedEventMsg{ThreadID: "parent-a", Event: protocol.TurnStartedWithID("voice-turn")})
	model.Update(ThreadScopedEventMsg{ThreadID: "parent-a", Event: protocol.ItemCompleted(sideDelegationItem("prompt-1"))})
	model.Update(ThreadScopedEventMsg{ThreadID: "parent-a", Event: protocol.AgentMessageDelta("commentary-1", "private commentary")})
	model.Update(ThreadScopedEventMsg{ThreadID: "parent-a", Event: protocol.ItemCompleted(protocol.AgentMessageItemWithPhase("commentary-1", "private commentary", "commentary"))})
	model.Update(ThreadScopedEventMsg{ThreadID: "parent-a", Event: protocol.AgentMessageDelta("final-1", "final answer")})
	model.Update(ThreadScopedEventMsg{ThreadID: "parent-a", Event: protocol.ItemCompleted(protocol.AgentMessageItemWithPhase("final-1", "final answer", "final_answer"))})

	resumeThreadForTest(model, "parent-a")
	openSideForTest(t, model)
	runTeaCmd(t, model, model.toggleSideConversation())
	view := model.View()
	if strings.Contains(view, "private commentary") {
		t.Fatalf("delegated parent turn leaked private commentary into the side snapshot:\n%s", view)
	}
	if !strings.Contains(view, "final answer") {
		t.Fatalf("delegated parent turn lost its final answer:\n%s", view)
	}
}

// Control case: a turn without a voice-handoff marker is untouched, so the
// suppression surface cannot grow into ordinary background work.
func TestSideParentSnapshotKeepsOrdinaryTurnLikeRust(t *testing.T) {
	model, _, _ := newSideNavigationModel(t, nil)
	openSideForTest(t, model)

	model.Update(ThreadScopedEventMsg{ThreadID: "parent-a", Event: protocol.TurnStartedWithID("typed-turn")})
	model.Update(ThreadScopedEventMsg{ThreadID: "parent-a", Event: protocol.AgentMessageDelta("commentary-1", "typed commentary")})
	model.Update(ThreadScopedEventMsg{ThreadID: "parent-a", Event: protocol.ItemCompleted(protocol.AgentMessageItemWithPhase("commentary-1", "typed commentary", "commentary"))})

	resumeThreadForTest(model, "parent-a")
	openSideForTest(t, model)
	runTeaCmd(t, model, model.toggleSideConversation())
	if view := model.View(); !strings.Contains(view, "typed commentary") {
		t.Fatalf("non-delegated parent turn lost its commentary:\n%s", view)
	}
}

// The side thread keeps consuming notifications while the user is back in the
// parent thread. A voice handoff there must be filtered in the retained side
// snapshot too (the Go counterpart of the side thread's own store).
func TestRetainedSideSnapshotHidesVoiceHandoffOutputLikeRust(t *testing.T) {
	model, _, _ := newSideNavigationModel(t, nil)
	openSideForTest(t, model)
	resumeThreadForTest(model, "thread-other")

	// Explicit turn ids: the contract path when the notification carries one.
	model.Update(ThreadScopedEventMsg{ThreadID: "side-parent-a", Event: protocol.TurnStartedWithID("side-voice-turn")})
	model.Update(ThreadScopedEventMsg{ThreadID: "side-parent-a", Event: protocol.ThreadEvent{
		Type: "item.completed", TurnID: "side-voice-turn", Item: &protocol.ThreadItem{ID: "prompt-1", Type: "user_message", Text: sideVoiceDelegationPrompt},
	}})
	model.Update(ThreadScopedEventMsg{ThreadID: "side-parent-a", Event: protocol.ThreadEvent{
		Type: "item.delta", TurnID: "side-voice-turn", Delta: &protocol.Delta{ItemID: "commentary-1", Text: "side private commentary"},
	}})
	model.Update(ThreadScopedEventMsg{ThreadID: "side-parent-a", Event: protocol.ThreadEvent{
		Type: "item.completed", TurnID: "side-voice-turn", Item: &protocol.ThreadItem{ID: "final-1", Type: "agent_message", Text: "side final answer", Phase: "final_answer"},
	}})

	resumeThreadForTest(model, "parent-a")
	openSideForTest(t, model)
	view := model.View()
	if strings.Contains(view, "side private commentary") {
		t.Fatalf("delegated side turn leaked private commentary:\n%s", view)
	}
	if !strings.Contains(view, "side final answer") {
		t.Fatalf("delegated side turn lost its final answer:\n%s", view)
	}
}

// Markers belong to one thread: a new side conversation for another parent must
// not inherit them even when it reuses the same turn id.
func TestSideVoiceMarkersDoNotLeakAcrossThreadsLikeRust(t *testing.T) {
	model, startCalls, _ := newSideNavigationModel(t, nil)
	openSideForTest(t, model)

	model.Update(ThreadScopedEventMsg{ThreadID: "parent-a", Event: protocol.TurnStartedWithID("voice-turn")})
	model.Update(ThreadScopedEventMsg{ThreadID: "parent-a", Event: protocol.ItemCompleted(sideDelegationItem("prompt-1"))})
	model.Update(ThreadScopedEventMsg{ThreadID: "parent-a", Event: protocol.AgentMessageDelta("commentary-1", "parent hidden commentary")})

	resumeThreadForTest(model, "parent-b")
	openSideForTest(t, model)
	if *startCalls != 2 {
		t.Fatalf("side starter calls = %d, want a fresh side for parent-b", *startCalls)
	}
	// Park the side thread in the background so its events go through the
	// snapshot path instead of the live transcript.
	runTeaCmd(t, model, model.toggleSideConversation())
	model.Update(ThreadScopedEventMsg{ThreadID: "side-parent-b", Event: protocol.TurnStartedWithID("voice-turn")})
	model.Update(ThreadScopedEventMsg{ThreadID: "side-parent-b", Event: protocol.AgentMessageDelta("commentary-1", "other thread commentary")})
	runTeaCmd(t, model, model.toggleSideConversation())
	if view := model.View(); !strings.Contains(view, "other thread commentary") {
		t.Fatalf("delegated marker leaked into another thread:\n%s", view)
	}

	// The original parent keeps its own marker.
	side := model.retainedSideConversationForParent("parent-a")
	if side == nil {
		t.Fatal("parent-a side conversation was not retained")
	}
	if messagesContainText(side.ParentMessages, "parent hidden commentary") {
		t.Fatalf("retained parent snapshot stored private commentary: %#v", side.ParentMessages)
	}
}
