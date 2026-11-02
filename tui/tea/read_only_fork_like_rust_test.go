package tea

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func lockedForkModel(t *testing.T, action SessionActionFunc) *Model {
	t.Helper()
	model := NewModel(codextui.NewState(nil), Options{
		Width:           120,
		Height:          40,
		OnSessionAction: action,
	})
	model.State.SetThreadID("thread-locked")
	model.setComposerPrompt("Draft to continue")
	model.setReadOnlyThread(true)
	if !model.readOnlyThread {
		t.Fatal("expected the locked conversation view")
	}
	return model
}

// Rust #47185: `f` in the locked conversation forks it, keeps the local draft,
// and replaces the summary with "Fork created. You can continue here.".
func TestLockedConversationForkPreservesDraftAndNoticeLikeRust(t *testing.T) {
	forks := 0
	model := lockedForkModel(t, func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
		forks++
		if selection.Kind != codextui.SessionSelectionFork {
			t.Fatalf("selection kind = %q, want fork", selection.Kind)
		}
		if got := strings.TrimSpace(selection.Target.ThreadID); got != "thread-locked" {
			t.Fatalf("fork source = %q, want thread-locked", got)
		}
		return &codextui.SessionSummary{ThreadID: "thread-fork", Title: "Fork"}, nil
	})
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "f fork") {
		t.Fatalf("locked footer does not advertise the fork shortcut:\n%s", view)
	}

	updated, _ := model.Update(keyRunes('f'))
	model = updated.(*Model)
	if forks != 1 {
		t.Fatalf("fork requests = %d, want 1", forks)
	}
	if model.readOnlyThread {
		t.Fatal("a successful fork must leave the locked view")
	}
	if got := model.State.ThreadID; got != "thread-fork" {
		t.Fatalf("thread after fork = %q, want thread-fork", got)
	}
	if got := model.composerText(); got != "Draft to continue" {
		t.Fatalf("draft in the fork = %q, want %q", got, "Draft to continue")
	}
	if got := model.notice; got != "Fork created. You can continue here." {
		t.Fatalf("fork notice = %q", got)
	}
}

// Rust #47185: a failed fork keeps the locked view and its draft unchanged.
func TestLockedConversationForkFailureKeepsLockedViewAndDraftLikeRust(t *testing.T) {
	model := lockedForkModel(t, func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
		return nil, errors.New("fork unavailable")
	})
	updated, _ := model.Update(keyRunes('F'))
	model = updated.(*Model)
	if !model.readOnlyThread {
		t.Fatal("a failed fork must keep the locked view")
	}
	if model.readOnlyDraft == nil || model.readOnlyDraft.text != "Draft to continue" {
		t.Fatalf("retained draft after failure = %#v", model.readOnlyDraft)
	}
	if got := model.State.ThreadID; got != "thread-locked" {
		t.Fatalf("thread after failed fork = %q", got)
	}
	if !strings.Contains(model.notice, "fork unavailable") {
		t.Fatalf("failure notice = %q", model.notice)
	}
	if model.forkInProgress {
		t.Fatal("a failed fork must release the input guard")
	}
}

// Rust #47185: while a fork is pending the notice is replaced by
// "Forking conversation…" and no key reaches the locked view.
func TestLockedConversationForkInProgressBlocksKeysLikeRust(t *testing.T) {
	retries := 0
	forks := 0
	model := NewModel(codextui.NewState(nil), Options{
		Width:  120,
		Height: 40,
		OnResumeSession: func(codextui.SessionSelection) (SessionResumeResponse, error) {
			retries++
			return SessionResumeResponse{}, nil
		},
		OnSessionAction: func(codextui.SessionSelection) (*codextui.SessionSummary, error) {
			forks++
			return &codextui.SessionSummary{ThreadID: "thread-fork"}, nil
		},
	})
	model.State.SetThreadID("thread-locked")
	model.setReadOnlyThread(true)
	model.forkInProgress = true

	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "Forking conversation\u2026") {
		t.Fatalf("fork progress line missing:\n%s", view)
	}
	if strings.Contains(view, "open in another app") {
		t.Fatalf("locked notice still rendered while forking:\n%s", view)
	}
	model.Update(keyRunes('r'))
	model.Update(keyRunes('f'))
	model.Update(keyRunes('x'))
	if retries != 0 || forks != 0 {
		t.Fatalf("pending fork accepted input (retries=%d forks=%d)", retries, forks)
	}
	if got := model.composerText(); got != "" {
		t.Fatalf("pending fork edited the draft: %q", got)
	}
}
