package tea

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// TestResumeFailureLeavesActiveThreadAndDraftLikeRust locks Rust #57342's
// guarantee that a failed resume never mutates the active thread or composer.
func TestResumeFailureLeavesActiveThreadAndDraftLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-active")
	model := NewModel(state, Options{
		Width:            100,
		Height:           30,
		ResumeCWD:        "current",
		SessionPickerCWD: "/current",
		OnResumeSession: func(codextui.SessionSelection) (SessionResumeResponse, error) {
			return SessionResumeResponse{}, errors.New("backend unavailable")
		},
	})
	model.composer.SetValue("keep this draft")
	beforeMessages := len(model.State.Messages)

	decision, notice, complete := model.applySessionSelection(codextui.SessionSelection{
		Kind: codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{
			ThreadID: "thread-other",
			CWD:      "/current",
		},
	})
	if decision != nil || !complete || !strings.Contains(notice, "Resume failed: backend unavailable") {
		t.Fatalf("decision=%#v notice=%q complete=%v", decision, notice, complete)
	}
	if model.State.ThreadID != "thread-active" {
		t.Fatalf("active thread = %q", model.State.ThreadID)
	}
	if got := model.composerText(); got != "keep this draft" {
		t.Fatalf("composer draft = %q", got)
	}
	if len(model.State.Messages) != beforeMessages {
		t.Fatalf("history changed: %d -> %d", beforeMessages, len(model.State.Messages))
	}
}
