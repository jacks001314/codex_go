package tea

import (
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	"codex_go/tui/keymsg"
)

// Rust #63193: a rewind restores the prompt while earlier keystrokes may still
// be buffered. The restored prompt must stay editable instead of being
// submitted (or re-entering browsing) by that typeahead.

func rewindGuardModel(t *testing.T, submits *int) (*Model, *time.Time) {
	t.Helper()
	current := time.Unix(1000, 0)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	state.AddMessage(codextui.RoleUser, "first")
	state.AddMessage(codextui.RoleAssistant, "one")
	state.AddMessage(codextui.RoleUser, "second")
	state.AddMessage(codextui.RoleAssistant, "two")
	onPromptEdit := func(tuiapp.PromptEditSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{Summary: &codextui.SessionSummary{ThreadID: "thread-reverted"}}, nil
	}
	model := NewModel(state, Options{
		Width:        80,
		Height:       24,
		OnPromptEdit: onPromptEdit,
		OnSubmit: func(string) bubbletea.Cmd {
			if submits != nil {
				*submits++
			}
			return nil
		},
	})
	model.now = func() time.Time { return current }
	return model, &current
}

func rewindToPrompt(t *testing.T, model *Model) {
	t.Helper()
	model.Update(key(keymsg.KeyEsc))
	model.Update(key(keymsg.KeyEsc))
	model.Update(key(keymsg.KeyEnter))
	if got := model.composer.Value(); got != "second" {
		t.Fatalf("composer = %q, want the restored prompt", got)
	}
}

func TestBufferedEnterAfterRewindDoesNotSubmitLikeRust(t *testing.T) {
	submits := 0
	model, current := rewindGuardModel(t, &submits)
	rewindToPrompt(t, model)

	model.Update(key(keymsg.KeyEnter))
	if submits != 0 {
		t.Fatalf("buffered Enter submitted the restored prompt (%d submits)", submits)
	}
	if got := model.composer.Value(); got != "second" {
		t.Fatalf("buffered Enter changed the restored draft: %q", got)
	}

	*current = current.Add(2 * selectExitGuardWindow)
	model.Update(key(keymsg.KeyEnter))
	if submits != 1 {
		t.Fatalf("fresh Enter submits = %d, want 1", submits)
	}
}

func TestThreadTransitionGuardConsumesEscapeAndExpiresLikeRust(t *testing.T) {
	model, current := rewindGuardModel(t, nil)
	rewindToPrompt(t, model)

	if !model.consumeThreadTransitionGuard(keymsg.KeyMsg{Type: keymsg.KeyEsc}) {
		t.Fatal("trailing Escape was not consumed after a rewind")
	}
	*current = current.Add(2 * selectExitGuardWindow)
	if model.consumeThreadTransitionGuard(keymsg.KeyMsg{Type: keymsg.KeyEsc}) {
		t.Fatal("guard did not expire after its window")
	}
	// Ordinary typing is never part of the guard window.
	if model.consumeThreadTransitionGuard(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'a'}}) {
		t.Fatal("guard swallowed a typed character")
	}
}

func TestLockedForkArmsTransitionGuardLikeRust(t *testing.T) {
	submits := 0
	model, _ := rewindGuardModel(t, &submits)
	model.forkInProgress = true
	model.applyForkCurrentSessionResult(forkCurrentSessionMsg{
		selection: codextui.SessionSelection{
			Kind:   codextui.SessionSelectionFork,
			Target: codextui.SessionTarget{ThreadID: "thread-1"},
		},
		sourceThreadID: "thread-1",
		origin:         forkOriginLocked,
		summary:        &codextui.SessionSummary{ThreadID: "thread-fork"},
	})
	if model.threadTransitionGuardUntil.IsZero() {
		t.Fatal("locked fork did not arm the typeahead guard")
	}
	if !model.consumeThreadTransitionGuard(keymsg.KeyMsg{Type: keymsg.KeyEnter}) {
		t.Fatal("locked fork guard did not consume a buffered Enter")
	}
}
