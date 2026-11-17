package tea

import (
	"testing"

	"codex_go/tui/keymsg"
)

// Rust app/side_focus.rs turns a horizontal list move into ChooserAction::Back
// once the pane has a selected chat: the chooser closes, the chat the pane was
// showing keeps it, and nothing new is attached.
func TestSidebarChooserHorizontalKeysReturnToSelectedChatLikeRust(t *testing.T) {
	for _, keyType := range []keymsg.KeyType{keymsg.KeyLeft, keymsg.KeyRight} {
		model, _ := newSidebarTabsModel(t)
		model.onListSideChats = func(string) ([]SideChatChoice, error) {
			return []SideChatChoice{{ThreadID: "saved-1"}, {ThreadID: "saved-2"}}, nil
		}
		attached := 0
		model.onOpenSideChat = func(parentThreadID, sideThreadID string) (AgentThreadSwitchResponse, error) {
			attached++
			return AgentThreadSwitchResponse{Status: "idle"}, nil
		}
		if cmd := model.openSidebarChooser(); cmd != nil {
			runTeaCmdChain(t, model, cmd, 0)
		}
		// The pane shows saved-1 while the chooser highlights saved-2.
		model.activeSide = &activeSideConversation{
			ParentThreadID: "parent-a",
			SideThreadID:   "saved-1",
			ShowingSide:    true,
		}
		model.State.SetThreadID("saved-1")
		model.sidebarChoice = 2

		_, cmd := model.Update(key(keyType))
		runTeaCmd(t, model, cmd)

		if model.sidebarChooserVisible() {
			t.Fatalf("%v did not leave the chooser", keyType)
		}
		if model.State.ThreadID != "saved-1" || attached != 0 {
			t.Fatalf("%v switched away: thread=%q attaches=%d", keyType, model.State.ThreadID, attached)
		}
	}
}

// Back cancels a pending attach before it leaves the chooser, so the footer
// keeps reading "cancel opening" and the late completion cannot switch chats
// (Rust side_focus.rs: opening.take() on a non-Failed OpenState).
func TestSidebarChooserHorizontalKeysCancelPendingOpenLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.onListSideChats = func(string) ([]SideChatChoice, error) {
		return []SideChatChoice{{ThreadID: "saved-1"}}, nil
	}
	opened := 0
	model.onOpenSideChat = func(parentThreadID, sideThreadID string) (AgentThreadSwitchResponse, error) {
		opened++
		return AgentThreadSwitchResponse{Status: "idle"}, nil
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	model.activeSide = &activeSideConversation{ParentThreadID: "parent-a", SideThreadID: "saved-1"}
	model.sidebarChoice = 1
	open := model.acceptSidebarChooser()
	if open == nil || model.sideOpenState != sideOpenLoading {
		t.Fatalf("open state = %q", model.sideOpenState)
	}
	late := open()

	_, cmd := model.Update(key(keymsg.KeyLeft))
	runTeaCmd(t, model, cmd)
	if !model.sidebarChooserVisible() {
		t.Fatal("horizontal Back closed the chooser while an attach was pending")
	}
	if model.sideOpeningThreadID != "" {
		t.Fatalf("cancel left opening state %q", model.sideOpeningThreadID)
	}
	model.Update(late)
	if model.State.ThreadID != "parent-a" || model.activeSide == nil || model.activeSide.SideThreadID != "saved-1" {
		t.Fatalf("cancelled open landed: thread=%q side=%#v", model.State.ThreadID, model.activeSide)
	}
	if opened != 1 {
		t.Fatalf("attaches = %d, want 1", opened)
	}
}

// Without a selected chat Rust's horizontal move falls through to
// apply_vertical_action, which ignores it: the key is consumed and the chooser
// stays where it was.
func TestSidebarChooserHorizontalKeysDoNothingWithoutSelectedChatLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.onListSideChats = func(string) ([]SideChatChoice, error) { return nil, nil }
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	if model.activeSide != nil {
		t.Fatal("test expects no selected side chat")
	}
	handled, cmd := model.updateSidebarChooserKey(key(keymsg.KeyLeft))
	if !handled || cmd != nil {
		t.Fatalf("horizontal move = handled %t cmd %v", handled, cmd != nil)
	}
	if !model.sidebarChooserVisible() || model.sidebarChoice != 0 {
		t.Fatalf("chooser moved: open=%t choice=%d", model.sidebarChooserVisible(), model.sidebarChoice)
	}
}
