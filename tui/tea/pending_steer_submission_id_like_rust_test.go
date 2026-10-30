package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// TestPendingSteerReceiptAcknowledgesByIdentityLikeRust covers Rust #42451:
// completion receipts for identical user messages must acknowledge the pending
// steer whose submission id they echo, not merely the first message with the
// same content.
func TestPendingSteerReceiptAcknowledgesByIdentityLikeRust(t *testing.T) {
	model := newPendingSteerModel(t, "thread-a", nil)
	firstID := submitPendingSteer(t, model, "same message")
	secondID := submitPendingSteer(t, model, "same message")
	if firstID == secondID {
		t.Fatalf("identical submissions reused client id %q", firstID)
	}

	// A receipt for an unknown submission with identical text must not consume
	// either pending steer.
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ThreadItem{
		ID:       "other-client",
		Type:     "user_message",
		Text:     "same message",
		ClientID: "other-client",
	})})
	if len(model.pendingSteers) != 2 {
		t.Fatalf("unknown receipt acknowledged a pending steer: %#v", model.pendingSteers)
	}
	if got := countRole(model.State.Messages, codextui.RoleUser); got != 0 {
		t.Fatalf("user messages after unknown receipt = %d, want 0", got)
	}

	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ThreadItem{
		ID:       firstID,
		Type:     "user_message",
		Text:     "same message",
		ClientID: firstID,
	})})
	if len(model.pendingSteers) != 1 || model.pendingSteers[0].ID != secondID {
		t.Fatalf("pending steers after first receipt = %#v", model.pendingSteers)
	}
	if got := countRole(model.State.Messages, codextui.RoleUser); got != 1 {
		t.Fatalf("user messages after first receipt = %d, want 1", got)
	}
}

// TestPendingSteerReceiptFallsBackToContentLikeRust covers the legacy path for
// app servers that do not echo submission ids.
func TestPendingSteerReceiptFallsBackToContentLikeRust(t *testing.T) {
	model := newPendingSteerModel(t, "thread-a", nil)
	submitPendingSteer(t, model, "legacy steer")

	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ThreadItem{
		ID:   "server-generated-item",
		Type: "user_message",
		Text: "legacy steer",
	})})
	if len(model.pendingSteers) != 0 {
		t.Fatalf("legacy receipt did not acknowledge the steer: %#v", model.pendingSteers)
	}
	if got := countRole(model.State.Messages, codextui.RoleUser); got != 1 {
		t.Fatalf("user messages after legacy receipt = %d, want 1", got)
	}
}

// TestInactiveThreadReceiptAcknowledgesParkedSteerLikeRust covers Rust #42451's
// acknowledgement for threads that are no longer displayed, before their
// notifications can be discarded.
func TestInactiveThreadReceiptAcknowledgesParkedSteerLikeRust(t *testing.T) {
	model := newPendingSteerModel(t, "thread-a", nil)
	clientID := submitPendingSteer(t, model, "parked steer")

	model.resetThreadScopedState()
	model.switchThreadID("thread-b")
	if len(model.pendingSteers) != 0 {
		t.Fatalf("pending steers leaked into thread-b: %#v", model.pendingSteers)
	}
	if pending := model.threadPendingSteers["thread-a"]; len(pending) != 1 || pending[0].ID != clientID {
		t.Fatalf("parked steer state = %#v", model.threadPendingSteers["thread-a"])
	}

	model.Update(ThreadScopedEventMsg{
		ThreadID: "thread-a",
		Event: protocol.ItemCompleted(protocol.ThreadItem{
			ID:       clientID,
			Type:     "user_message",
			Text:     "parked steer",
			ClientID: clientID,
		}),
	})
	if pending := model.threadPendingSteers["thread-a"]; len(pending) != 0 {
		t.Fatalf("inactive receipt left parked steer state: %#v", pending)
	}

	// Returning to the thread must not resurrect the acknowledged steer.
	model.switchThreadID("thread-a")
	if len(model.pendingSteers) != 0 {
		t.Fatalf("acknowledged steer was restored: %#v", model.pendingSteers)
	}
	if got := countRole(model.State.Messages, codextui.RoleUser); got != 0 {
		t.Fatalf("user messages after inactive acknowledgement = %d, want 0", got)
	}
}

// TestPendingSteerSurvivesThreadSwitchLikeRust covers the ThreadInputState half
// of #42451: switching away parks the pending steer with its id, and switching
// back restores it so a later receipt still commits it.
func TestPendingSteerSurvivesThreadSwitchLikeRust(t *testing.T) {
	model := newPendingSteerModel(t, "thread-a", nil)
	clientID := submitPendingSteer(t, model, "surviving steer")

	model.resetThreadScopedState()
	model.switchThreadID("thread-b")
	model.switchThreadID("thread-a")
	if len(model.pendingSteers) != 1 || model.pendingSteers[0].ID != clientID {
		t.Fatalf("restored pending steers = %#v", model.pendingSteers)
	}

	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ThreadItem{
		ID:       clientID,
		Type:     "user_message",
		Text:     "surviving steer",
		ClientID: clientID,
	})})
	if len(model.pendingSteers) != 0 {
		t.Fatalf("pending steers after restored receipt = %#v", model.pendingSteers)
	}
	if got := countRole(model.State.Messages, codextui.RoleUser); got != 1 {
		t.Fatalf("user messages after restored receipt = %d, want 1", got)
	}
}

// TestSubmitRequestAssignsUniqueClientMessageID covers Rust #42451's "assign
// each TUI submission a client message id" half of the change.
func TestSubmitRequestAssignsUniqueClientMessageID(t *testing.T) {
	var submitted []SubmitRequest
	state := codextui.NewState(nil)
	state.SetThreadID("thread-submit")
	model := NewModel(state, Options{
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
	})
	model.submitRequest(SubmitRequest{Prompt: "first"}, false)
	model.submitRequest(SubmitRequest{Prompt: "second"}, false)
	if len(submitted) != 2 {
		t.Fatalf("submitted requests = %d, want 2", len(submitted))
	}
	if submitted[0].ClientID == "" || submitted[1].ClientID == "" {
		t.Fatalf("submission client ids = %q / %q", submitted[0].ClientID, submitted[1].ClientID)
	}
	if submitted[0].ClientID == submitted[1].ClientID {
		t.Fatalf("submissions reused client id %q", submitted[0].ClientID)
	}
}

func newPendingSteerModel(t *testing.T, threadID string, onSteer func(SubmitRequest, string) error) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID(threadID)
	state.SetStatus("running")
	if onSteer == nil {
		onSteer = func(SubmitRequest, string) error { return nil }
	}
	return NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnSubmitRequest: func(SubmitRequest) bubbletea.Cmd {
			return nil
		},
		OnSteerRequest: onSteer,
	})
}

func submitPendingSteer(t *testing.T, model *Model, text string) string {
	t.Helper()
	before := len(model.pendingSteers)
	model.composer.SetValue(text)
	if cmd := model.steerComposer(); cmd != nil {
		_ = cmd()
	}
	if len(model.pendingSteers) != before+1 {
		t.Fatalf("pending steers = %d, want %d", len(model.pendingSteers), before+1)
	}
	return model.pendingSteers[len(model.pendingSteers)-1].ID
}
