package bottompane

import (
	"testing"
)

// Rust #50756 (cd7d9e128c, codex-rs/tui/src/bottom_pane/command_popup.rs):
// mirrors `unavailable_side_conversation_command_is_shown_only_when_searched`.
func TestCommandPopupSideUnavailableCommandShownOnlyWhenSearchedLikeRust(t *testing.T) {
	popup := NewCommandPopup(CommandPopupFlags{SideConversationActive: true}, nil)
	popup.OnComposerTextChange("/")
	if names := commandPopupItemNames(popup.FilteredItems()); containsString(names, "archive") {
		t.Fatalf("archive must stay hidden from the unfiltered side menu: %#v", names)
	}

	popup.OnComposerTextChange("/arch")
	items := popup.FilteredItems()
	if len(items) != 1 || items[0].Name != "archive" || !items[0].Unavailable {
		t.Fatalf("filtered side /arch = %#v, want exactly one disabled archive", items)
	}
	if selected, ok := popup.SelectedItem(); ok {
		t.Fatalf("disabled archive must not be selectable, selected=%#v", selected)
	}
	rows := commandPopupDisplayRows(items)
	if len(rows) != 1 || !rows[0].IsDisabled || rows[0].DisabledReason != "not available in a side conversation" {
		t.Fatalf("display rows = %#v, want one disabled row with the side reason", rows)
	}

	// Available matches rank ahead of the disabled ones and stay selectable.
	popup.OnComposerTextChange("/s")
	items = popup.FilteredItems()
	if len(items) < 2 || items[0].Name != "status" || items[0].Unavailable {
		t.Fatalf("filtered side /s = %#v, want available status first", items)
	}
	popup.MoveUp()
	if selected, ok := popup.SelectedItem(); !ok || selected.Name != "status" {
		t.Fatalf("selection after MoveUp = %#v ok=%v, want available status", selected, ok)
	}
}
