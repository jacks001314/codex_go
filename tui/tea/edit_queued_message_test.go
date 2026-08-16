package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// These tests mirror Rust tui/src/chatwidget/tests/composer_submission.rs for
// 2bc43d516e (#38907 "Edit queued messages with Vim history-up") and its
// prerequisite edit_queued_message action (alt-up): the composer restores the
// latest queued follow-up for editing and removes it from the queue, so
// submitting the edited version replaces it instead of creating a duplicate.

func newEditQueuedMessageModel() *Model {
	return NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36})
}

func keyRunes(r rune) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{r}}
}

// TestEditQueuedMessageAltUpRestoresMostRecentLikeRust mirrors Rust
// alt_up_edits_most_recent_queued_message: the chat edit_queued_message
// binding (default alt-up) restores the most recent queued message into the
// composer and leaves the older queue entry in place.
func TestEditQueuedMessageAltUpRestoresMostRecentLikeRust(t *testing.T) {
	model := newEditQueuedMessageModel()
	model.composer.SetValue("first queued")
	model.queueComposer(false)
	model.composer.SetValue("second queued")
	model.queueComposer(false)
	if len(model.queued) != 2 {
		t.Fatalf("seeded queue = %d, want 2", len(model.queued))
	}

	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)

	if got := model.composer.Value(); got != "second queued" {
		t.Fatalf("composer = %q, want the most recent queued message", got)
	}
	if len(model.queued) != 1 || model.queued[0].Request.Prompt != "first queued" {
		t.Fatalf("queue = %#v, want only the older message", model.queued)
	}
}

// TestEditQueuedMessageVimHistoryUpEditAndRequeueCycleLikeRust mirrors Rust
// vim_normal_history_up_edits_queued_message_without_duplicating_it: in Vim
// normal mode an empty composer's history-up binding (vim_normal move_up,
// default k) restores the queued follow-up and removes it from the queue;
// editing and requeueing repeatedly keeps exactly one queue entry.
func TestEditQueuedMessageVimHistoryUpEditAndRequeueCycleLikeRust(t *testing.T) {
	model := newEditQueuedMessageModel()
	model.vimMode = true

	model.composer.SetValue("first queued message")
	model.queueComposer(false)
	if len(model.queued) != 1 {
		t.Fatalf("queue after first submit = %d, want 1", len(model.queued))
	}

	for _, expected := range []string{"first queued message", "first queued message edited"} {
		updated, _ := model.Update(keyRunes('k'))
		model = updated.(*Model)
		if got := model.composer.Value(); got != expected {
			t.Fatalf("composer after history-up = %q, want %q", got, expected)
		}
		if len(model.queued) != 0 {
			t.Fatalf("queue after restore = %d, want 0 (restored message removed)", len(model.queued))
		}

		model.composer.SetValue(expected + " edited")
		model.queueComposer(false)
		if len(model.queued) != 1 {
			t.Fatalf("queue after requeue = %d, want 1 (no duplicate)", len(model.queued))
		}
	}
	if got := model.queued[0].Request.Prompt; got != "first queued message edited edited" {
		t.Fatalf("final queued prompt = %q, want first queued message edited edited", got)
	}
}

// TestEditQueuedMessageVimHistoryUpUsesRemappedBindingLikeRust mirrors Rust
// vim_normal_queued_message_edit_uses_remapped_history_up: a remapped
// vim_normal move_up binding (F2) triggers the queued-message restore.
func TestEditQueuedMessageVimHistoryUpUsesRemappedBindingLikeRust(t *testing.T) {
	config := codextui.NewKeymapConfig()
	if err := config.Set("vim_normal", "move_up", []string{"f2"}); err != nil {
		t.Fatalf("set vim_normal move_up remap: %v", err)
	}
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36, KeymapConfig: config})
	model.vimMode = true
	model.composer.SetValue("queued message")
	model.queueComposer(false)

	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF2})
	model = updated.(*Model)
	if got := model.composer.Value(); got != "queued message" {
		t.Fatalf("composer after remapped F2 = %q, want queued message", got)
	}
	if len(model.queued) != 0 {
		t.Fatalf("queue after F2 restore = %d, want 0", len(model.queued))
	}
}

// TestEditQueuedMessageKeepsHistoryNavigationWhenComposerHasText pins the
// #38907 guard: with Vim mode on but a non-empty composer, the vim_normal
// move_up binding must NOT restore a queued message (normal composer behavior
// is preserved).
func TestEditQueuedMessageKeepsHistoryNavigationWhenComposerHasText(t *testing.T) {
	model := newEditQueuedMessageModel()
	model.vimMode = true
	model.composer.SetValue("queued message")
	model.queueComposer(false)
	model.composer.SetValue("draft text")

	updated, _ := model.Update(keyRunes('k'))
	model = updated.(*Model)
	if len(model.queued) != 1 {
		t.Fatalf("queue changed while composer has text: %#v", model.queued)
	}
}
