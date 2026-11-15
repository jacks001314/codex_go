package tea

import (
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

func pendingBackendSwitchModel(t *testing.T) *Model {
	t.Helper()
	from := codextui.NewBackendID()
	owner := codextui.NewBackendID()
	model := backendSwitchModel(t, from, func(BackendSwitchRequest) (BackendSwitchResult, error) {
		return BackendSwitchResult{Response: backendSwitchResponse()}, nil
	}, nil)
	if decision, notice, complete := model.applySessionSelection(backendSwitchSelection(owner)); decision != nil || complete || notice != "" {
		t.Fatalf("switch start decision=%#v notice=%q complete=%v", decision, notice, complete)
	}
	if model.pendingBackendSwitch == nil {
		t.Fatal("switch did not start")
	}
	return model
}

// TestBackendSwitchKeepsDraftEditableLikeRust locks Rust #61481's responsive
// handoff: the composer keeps accepting edits while the destination backend is
// prepared, but Enter cannot submit and app shortcuts wait.
func TestBackendSwitchKeepsDraftEditableLikeRust(t *testing.T) {
	model := pendingBackendSwitchModel(t)
	submitted := 0
	model.onSubmit = func(string) bubbletea.Cmd {
		submitted++
		return nil
	}

	typeText(t, model, "abc")
	if got := model.composerText(); got != "keep this draftabc" {
		t.Fatalf("composer draft during switch = %q", got)
	}
	model.Update(key(keymsg.KeyEnter))
	if submitted != 0 {
		t.Fatalf("submitted %d prompts during the handoff", submitted)
	}
	if got := model.composerText(); got != "keep this draftabc" {
		t.Fatalf("Enter changed the draft to %q", got)
	}
	if model.pendingBackendSwitch == nil {
		t.Fatal("editing ended the pending switch")
	}
}

// TestBackendSwitchCtrlCClearsDraftThenQuitsLikeRust locks Rust #61481:
// Ctrl+C clears buffered draft input first, and a second press with no draft,
// modal or popup abandons the switch and requests shutdown.
func TestBackendSwitchCtrlCClearsDraftThenQuitsLikeRust(t *testing.T) {
	model := pendingBackendSwitchModel(t)
	stateID := model.pendingBackendSwitch.id

	_, cmd := model.Update(key(keymsg.KeyCtrlC))
	if cmd != nil {
		if _, quit := cmd().(bubbletea.QuitMsg); quit {
			t.Fatal("Ctrl+C quit before clearing the draft")
		}
	}
	if got := model.composerText(); got != "" {
		t.Fatalf("draft after Ctrl+C = %q", got)
	}
	if model.pendingBackendSwitch == nil {
		t.Fatal("clearing the draft abandoned the switch")
	}

	updated, quitCmd := model.Update(key(keymsg.KeyCtrlC))
	model = updated.(*Model)
	if quitCmd == nil {
		t.Fatal("empty Ctrl+C during a switch returned no command")
	}
	if _, ok := quitCmd().(bubbletea.QuitMsg); !ok {
		t.Fatalf("empty Ctrl+C returned %T, want QuitMsg", quitCmd())
	}
	if model.pendingBackendSwitch != nil {
		t.Fatal("quit kept the pending switch")
	}

	// The abandoned preparation may finish after the quit request; its result
	// must not replace the displayed conversation.
	model.Update(backendSwitchResultMsg{id: stateID, result: BackendSwitchResult{Response: backendSwitchResponse()}})
	if model.State.ThreadID != "thread-source" {
		t.Fatalf("abandoned result displayed thread %q", model.State.ThreadID)
	}
	if len(model.State.Messages) != 1 || model.State.Messages[0].Text != "source history" {
		t.Fatalf("abandoned result replaced the transcript: %#v", model.State.Messages)
	}
}

// TestBackendSwitchSecondSelectionWaitsLikeRust locks the single-navigation
// owner rule: another picker selection reports the pending handoff instead of
// starting a competing destination.
func TestBackendSwitchSecondSelectionWaitsLikeRust(t *testing.T) {
	model := pendingBackendSwitchModel(t)
	decision, notice, complete := model.applySessionSelection(backendSwitchSelection(codextui.NewBackendID()))
	if decision != nil || !complete || notice != "Wait for the current session switch to finish." {
		t.Fatalf("second selection decision=%#v notice=%q complete=%v", decision, notice, complete)
	}
	if model.pendingBackendSwitch == nil {
		t.Fatal("second selection dropped the pending switch")
	}
}

// dropMessages runs a command and every nested batch member, feeding results
// back through Update until the handoff result has been applied.
func dropMessages(t *testing.T, model *Model, cmd bubbletea.Cmd) *Model {
	t.Helper()
	if cmd == nil {
		return model
	}
	msg := cmd()
	if batch, ok := msg.(bubbletea.BatchMsg); ok {
		for _, sub := range batch {
			model = dropMessages(t, model, sub)
		}
		return model
	}
	if msg == nil {
		return model
	}
	updated, next := model.Update(msg)
	model = updated.(*Model)
	return dropMessages(t, model, next)
}

// TestBackendSwitchPickerCompletesOffLoopLikeRust locks the production path: a
// picker selection starts the handoff as a command, the picker closes
// immediately, and the modal response is delivered only after adoption.
func TestBackendSwitchPickerCompletesOffLoopLikeRust(t *testing.T) {
	from := codextui.NewBackendID()
	owner := codextui.NewBackendID()
	var responses []ModalResponse
	model := NewModel(codextui.NewState(nil), Options{
		Width:     100,
		Height:    30,
		BackendID: from,
		SessionPickerItems: []codextui.SessionSummary{{
			ThreadID:  "thread-dest",
			BackendID: owner,
			Title:     "Destination",
			CWD:       "/dest",
		}},
		OnSwitchBackendSession: func(BackendSwitchRequest) (BackendSwitchResult, error) {
			return BackendSwitchResult{Response: backendSwitchResponse()}, nil
		},
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	model.switchThreadID("thread-source")
	model.openSessionPicker(codextui.SessionPickerResume)
	if model.modal == nil || model.modal.sessionPicker == nil {
		t.Fatal("resume picker did not open")
	}

	updated, cmd := model.Update(key(keymsg.KeyEnter))
	model = updated.(*Model)
	if model.modal != nil {
		t.Fatal("picker stayed open during the handoff")
	}
	if model.pendingBackendSwitch == nil {
		t.Fatal("picker selection did not start the handoff")
	}
	if len(responses) != 0 {
		t.Fatalf("modal response delivered before adoption: %#v", responses)
	}

	model = dropMessages(t, model, cmd)
	if model.pendingBackendSwitch != nil {
		t.Fatal("handoff stayed pending after its command ran")
	}
	if model.State.ThreadID != "thread-dest" || model.backendID != owner {
		t.Fatalf("adopted thread/backend = %q/%v", model.State.ThreadID, model.backendID)
	}
	if len(responses) != 1 {
		t.Fatalf("modal responses = %d, want 1", len(responses))
	}
	if responses[0].Picker == nil || responses[0].Picker.Value != "thread-dest" {
		t.Fatalf("modal picker response = %#v", responses[0].Picker)
	}
}
