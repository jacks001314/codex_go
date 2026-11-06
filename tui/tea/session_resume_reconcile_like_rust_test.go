package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// TestResumeReconcilesPendingSteersByClientIDLikeRust covers Rust #59817:
// a resumed session acknowledges the submitted messages its history contains.
// Identical text with distinct client ids stays distinct, so only the id in
// history leaves the pending queue.
func TestResumeReconcilesPendingSteersByClientIDLikeRust(t *testing.T) {
	model := newPendingSteerModel(t, "thread-a", nil)
	firstID := submitPendingSteer(t, model, "same message")
	secondID := submitPendingSteer(t, model, "same message")
	if firstID == secondID {
		t.Fatalf("identical submissions reused client id %q", firstID)
	}

	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Messages: []codextui.Message{
			{Role: codextui.RoleUser, Text: "same message", ClientID: secondID},
			// A non-user entry that happens to carry the id must not confirm it.
			{Role: codextui.RoleAssistant, Text: "working", ClientID: firstID},
		},
	})
	if len(model.pendingSteers) != 1 || model.pendingSteers[0].ID != firstID {
		t.Fatalf("pending steers after resume = %#v, want only %q", model.pendingSteers, firstID)
	}
	if got := countRole(model.State.Messages, codextui.RoleUser); got != 1 {
		t.Fatalf("replayed user messages = %d, want 1", got)
	}
}

// TestResumeReconcilesParkedSteersByClientIDLikeRust covers the ThreadInputState
// half: a pending steer parked while the view displayed another thread is
// acknowledged by the resumed thread's history instead of coming back.
func TestResumeReconcilesParkedSteersByClientIDLikeRust(t *testing.T) {
	model := newPendingSteerModel(t, "thread-a", nil)
	clientID := submitPendingSteer(t, model, "parked steer")
	model.resetThreadScopedState()
	model.switchThreadID("thread-b")
	if pending := model.threadPendingSteers["thread-a"]; len(pending) != 1 {
		t.Fatalf("parked steers = %#v", pending)
	}

	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Messages: []codextui.Message{{Role: codextui.RoleUser, Text: "parked steer", ClientID: clientID}},
	})
	if len(model.pendingSteers) != 0 {
		t.Fatalf("pending steers after resume = %#v", model.pendingSteers)
	}
	if pending := model.threadPendingSteers["thread-a"]; len(pending) != 0 {
		t.Fatalf("parked steers after resume = %#v", pending)
	}
}

// TestResumeReconcilesQueuedFollowUpsByClientIDLikeRust covers the queued-input
// half of Rust's reconcile_confirmed_messages: a follow-up whose client id the
// resumed history reports must not be submitted a second time.
func TestResumeReconcilesQueuedFollowUpsByClientIDLikeRust(t *testing.T) {
	model := newPendingSteerModel(t, "thread-a", nil)
	model.queued = []queuedSubmission{
		{Request: SubmitRequest{Prompt: "already landed", ClientID: "confirmed-id"}},
		{Request: SubmitRequest{Prompt: "still waiting", ClientID: "waiting-id"}},
	}

	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Messages: []codextui.Message{{Role: codextui.RoleUser, Text: "already landed", ClientID: "confirmed-id"}},
	})
	if len(model.queued) != 1 || model.queued[0].Request.ClientID != "waiting-id" {
		t.Fatalf("queued submissions after resume = %#v", model.queued)
	}
	if len(model.rejectedSteers) != 0 {
		t.Fatalf("rejected steers after resume = %#v", model.rejectedSteers)
	}
}
