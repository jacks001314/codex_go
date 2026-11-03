package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// Rust #47320 (app_backtrack/legacy_input.rs): with the transcript overlay open
// in a conversation locked by another app, Escape closes the overlay instead of
// starting a prompt backtrack preview, and the retained draft is untouched.
func TestReadOnlyTranscriptOverlayEscapeClosesWithoutBacktrackLikeRust(t *testing.T) {
	model, _ := readOnlyResumeModel(t, nil)
	model.State.SetThreadID("thread-a")
	model.setComposerPrompt("Retained draft")
	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-a"},
		Messages: []codextui.Message{{Role: codextui.RoleUser, Text: "Existing prompt"}},
		ReadOnly: true,
	})
	if !model.readOnlyThread {
		t.Fatal("expected the external-writer view")
	}
	if got := model.composerText(); got != "" {
		t.Fatalf("locked view kept a visible draft: %q", got)
	}
	model.openTranscriptOverlay()
	if model.overlay == nil {
		t.Fatal("transcript overlay did not open")
	}

	updated, cmd := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	model = updated.(*Model)
	if cmd != nil {
		// Closing an alt-screen overlay may return terminal commands; nothing
		// else may be scheduled from the Escape press.
		t.Logf("escape command = %T", cmd)
	}
	if model.overlay != nil {
		t.Fatal("Escape must close the locked transcript overlay")
	}
	if model.backtrack.Primed || model.backtrack.OverlayPreviewActive {
		t.Fatalf("locked Escape started backtrack (primed=%v preview=%v)", model.backtrack.Primed, model.backtrack.OverlayPreviewActive)
	}
	if !model.readOnlyThread {
		t.Fatal("closing the overlay must keep the locked view")
	}
	// The locked view hides the composer, so the draft lives in the retained
	// state until a retry or fork restores it (see the retry test below).
	if model.readOnlyDraft == nil || model.readOnlyDraft.text != "Retained draft" {
		t.Fatalf("retained draft = %#v, want %q", model.readOnlyDraft, "Retained draft")
	}
	if got := model.composerText(); got != "" {
		t.Fatalf("locked composer = %q, want hidden", got)
	}
}

// Rust #47320 (app/input.rs): should_handle_backtrack_esc excludes external
// writer views, so a locked conversation never primes prompt editing.
func TestShouldHandleBacktrackEscRejectsExternalWriterLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	if !model.shouldHandleBacktrackEsc() {
		t.Fatal("an idle editable composer should handle Escape for backtracking")
	}
	model.State.SetThreadID("thread-locked")
	model.setReadOnlyThread(true)
	if model.shouldHandleBacktrackEsc() {
		t.Fatal("a locked external-writer view must not handle Escape for backtracking")
	}
	if got := model.readOnlyDraft; got != nil {
		t.Fatalf("an empty draft left a retained state: %#v", got)
	}
}

// Rust #43253/#47320: the locked view hides the composer but keeps the draft so
// a successful retry resumes with the same input.
func TestReadOnlyRetryRestoresRetainedDraftLikeRust(t *testing.T) {
	model, _ := readOnlyResumeModel(t, []SessionResumeResponse{{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-a"},
		Messages: []codextui.Message{{Role: codextui.RoleAssistant, Text: "history"}},
	}})
	model.State.SetThreadID("thread-a")
	model.setComposerPrompt("Retained draft")
	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-a"},
		ReadOnly: true,
	})
	if !model.readOnlyThread {
		t.Fatal("expected the external-writer view")
	}
	updated, _ := model.Update(keyRunes('r'))
	model = updated.(*Model)
	if model.readOnlyThread {
		t.Fatal("a successful retry must clear the locked view")
	}
	if got := model.composerText(); got != "Retained draft" {
		t.Fatalf("draft after retry = %q, want %q", got, "Retained draft")
	}
	if model.readOnlyDraft != nil {
		t.Fatal("the retained draft must be consumed by the retry")
	}
}
