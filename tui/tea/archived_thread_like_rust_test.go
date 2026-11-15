package tea

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

func archivedThreadModel(t *testing.T) (*Model, *int, *int) {
	t.Helper()
	resumed := 0
	unarchived := 0
	model := NewModel(codextui.NewState(nil), Options{
		Width:  120,
		Height: 40,
		OnUnarchiveThread: func(threadID string) error {
			unarchived++
			return nil
		},
		OnResumeSession: func(selection codextui.SessionSelection) (SessionResumeResponse, error) {
			resumed++
			return SessionResumeResponse{Summary: &codextui.SessionSummary{ThreadID: selection.Target.ThreadID}}, nil
		},
	})
	model.State.SetThreadID("thread-archived")
	return model, &resumed, &unarchived
}

// Rust #61486: an archived conversation keeps its cached transcript but
// replaces the composer with a notice that advertises U.
func TestArchivedThreadNoticeLikeRust(t *testing.T) {
	model, _, _ := archivedThreadModel(t)
	model.setArchivedThread(true)
	view := utils.StripANSI(model.viewString())
	for _, want := range []string{
		"This thread was archived",
		"u to unarchive",
		"Press U to unarchive this thread and continue here.",
		"u unarchive",
		"Ctrl+T transcript",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("archived notice missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"r to retry", "f fork", "This conversation is open in another app"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("archived notice kept %q:\n%s", unwanted, view)
		}
	}
}

// Rust #61486 app/input.rs: U unarchives and reattaches the displayed thread,
// while R and F no longer recover it.
func TestArchivedThreadKeysLikeRust(t *testing.T) {
	model, resumed, unarchived := archivedThreadModel(t)
	model.setArchivedThread(true)

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'r'}})
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'f'}})
	if *resumed != 0 || model.forkInProgress {
		t.Fatalf("archived view reacted to a recovery key: resumed=%d fork=%v", *resumed, model.forkInProgress)
	}
	if !model.archivedView() {
		t.Fatal("archived view cleared without an unarchive")
	}

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'u'}})
	if *unarchived != 1 || *resumed != 1 {
		t.Fatalf("U unarchive=%d resume=%d, want 1/1", *unarchived, *resumed)
	}
	if model.archivedView() || model.readOnlyThread {
		t.Fatal("U did not leave the archived read-only view")
	}
}

// Rust #61486: a failed unarchive keeps the archived view and its retained
// draft instead of clearing the conversation.
func TestArchivedThreadUnarchiveFailureKeepsDraftLikeRust(t *testing.T) {
	model, resumed, _ := archivedThreadModel(t)
	model.setComposerPrompt("Retained draft")
	model.setArchivedThread(true)
	model.onUnarchiveThread = func(string) error { return errors.New("unarchive denied") }
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'u'}})
	if *resumed != 0 || !model.archivedView() {
		t.Fatalf("failed unarchive changed the view: resumed=%d archived=%v", *resumed, model.archivedView())
	}
	if model.readOnlyDraft == nil || model.readOnlyDraft.text != "Retained draft" {
		t.Fatalf("retained draft = %#v, want %q", model.readOnlyDraft, "Retained draft")
	}
	if !strings.Contains(utils.StripANSI(model.viewString()), "unarchive denied") {
		t.Fatalf("failure notice missing:\n%s", utils.StripANSI(model.viewString()))
	}
}

// Rust #61486 app/archived_thread.rs: archiving the displayed thread freezes
// it, and another client's unarchive reattaches it automatically.
func TestArchivedThreadNotificationsLikeRust(t *testing.T) {
	model, resumed, _ := archivedThreadModel(t)
	model.setComposerPrompt("draft before archive")
	model.Update(AgentThreadVisibilityMsg{ThreadID: "thread-archived", Archived: true})
	if !model.archivedView() {
		t.Fatal("archive notification did not freeze the displayed thread")
	}
	if model.readOnlyDraft == nil || model.readOnlyDraft.text != "draft before archive" {
		t.Fatalf("archive notification dropped the draft: %#v", model.readOnlyDraft)
	}
	if !strings.Contains(utils.StripANSI(model.viewString()), "This thread was archived") {
		t.Fatalf("archive notice missing:\n%s", utils.StripANSI(model.viewString()))
	}

	model.Update(AgentThreadVisibilityMsg{ThreadID: "thread-archived", Archived: false})
	if *resumed != 1 || model.archivedView() {
		t.Fatalf("external unarchive resumed=%d archived=%v, want 1/false", *resumed, model.archivedView())
	}
}

// Rust #61486 preserves the archived state across thread selection.
func TestArchivedThreadSelectionLikeRust(t *testing.T) {
	model, _, _ := archivedThreadModel(t)
	model.archivedAgentThreads = map[string]bool{"thread-old": true}
	model.switchThreadID("thread-old")
	if !model.archivedView() {
		t.Fatal("selecting a cached archived thread did not freeze the view")
	}
	model.switchThreadID("thread-live")
	if model.archivedView() || model.readOnlyThread {
		t.Fatal("selecting a live thread kept the archived view")
	}
}

// Rust #61486 app/reconnect.rs: a resume rejected as archived turns the cached
// view into the archived notice instead of a retry failure.
func TestRetryArchivedResumeShowsArchivedViewLikeRust(t *testing.T) {
	model, _, _ := archivedThreadModel(t)
	model.onResumeSession = func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{}, errors.New("session thread-archived is archived. Run `codex unarchive thread-archived` to unarchive it first.")
	}
	model.setReadOnlyThread(true)
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'r'}})
	if !model.archivedView() {
		t.Fatal("archived resume rejection did not switch to the archived view")
	}
	if !strings.Contains(utils.StripANSI(model.viewString()), "This thread was archived") {
		t.Fatalf("archived notice missing after retry:\n%s", utils.StripANSI(model.viewString()))
	}
}
