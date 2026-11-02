package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// runLockedForkCmd drains the deferred fork command the way bubbletea does:
// the RPC runs off the update loop and its result arrives as a message.
func runLockedForkCmd(t *testing.T, model *Model, cmd bubbletea.Cmd) *Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("fork scheduled no command")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("fork command returned no message")
	}
	updated, _ := model.Update(msg)
	model = updated.(*Model)
	return model
}

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

	updated, cmd := model.Update(keyRunes('f'))
	model = updated.(*Model)
	// Rust #47185: the progress line is painted before the request leaves, so
	// the view must already read "Forking conversation…" while the RPC is pending.
	if !model.forkInProgress {
		t.Fatal("locked fork did not enter the in-progress state")
	}
	if forks != 0 {
		t.Fatalf("fork RPC ran inside the key handler (requests=%d before command)", forks)
	}
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "Forking conversation\u2026") {
		t.Fatalf("progress line missing while the fork is pending:\n%s", view)
	} else if strings.Contains(view, "open in another app") {
		t.Fatalf("locked notice still rendered while forking:\n%s", view)
	}
	model = runLockedForkCmd(t, model, cmd)
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
	updated, cmd := model.Update(keyRunes('F'))
	model = updated.(*Model)
	model = runLockedForkCmd(t, model, cmd)
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

// The deferred fork only reads captured values: dropping its message (TUI
// quit) or never delivering it cannot touch the model, so there is no goroutine
// leak into a torn-down Program and no write to a destroyed model.
func TestLockedForkCommandOutlivesDroppedModelLikeRust(t *testing.T) {
	release := make(chan struct{})
	done := make(chan struct{})
	model := lockedForkModel(t, func(codextui.SessionSelection) (*codextui.SessionSummary, error) {
		<-release
		close(done)
		return &codextui.SessionSummary{ThreadID: "thread-fork"}, nil
	})
	updated, cmd := model.Update(keyRunes('f'))
	model = updated.(*Model)
	if !model.forkInProgress {
		t.Fatal("expected the pending fork state")
	}
	var msg bubbletea.Msg
	finished := make(chan struct{})
	go func() {
		msg = cmd()
		close(finished)
	}()
	// The abandoned TUI keeps rendering the progress line; keys stay with the
	// fork (Rust #47185) and the model is untouched by the background command.
	before := model.View()
	model.Update(keyRunes('x'))
	if got := model.composerText(); got != "" {
		t.Fatalf("pending fork accepted a keystroke: %q", got)
	}
	close(release)
	<-finished
	<-done
	if msg == nil {
		t.Fatal("fork command produced no message")
	}
	if model.readOnlyThread != true || !model.forkInProgress {
		t.Fatal("dropping the result must leave the model untouched")
	}
	if after := model.View(); after != before {
		t.Fatalf("background fork mutated the model:\nbefore=%q\nafter=%q", before, after)
	}
}
