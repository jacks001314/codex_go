package tea

import (
	"testing"

	"codex_go/tui/keymsg"
)

// Rust #60599: opening a saved chat runs in the background, keeps the chooser
// usable, and only the newest selection may switch conversations.
func TestSavedChatOpenSupersedesStaleCompletionLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.onListSideChats = func(string) ([]SideChatChoice, error) {
		return []SideChatChoice{{ThreadID: "saved-1"}, {ThreadID: "saved-2"}}, nil
	}
	model.onOpenSideChat = func(parentThreadID, sideThreadID string) (AgentThreadSwitchResponse, error) {
		return AgentThreadSwitchResponse{Status: "idle"}, nil
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	model.sidebarChoice = 1
	first := model.acceptSidebarChooser()
	if first == nil || model.sideOpeningThreadID != "saved-1" {
		t.Fatalf("first open state = %q", model.sideOpeningThreadID)
	}
	if !model.sidebarChooserVisible() {
		t.Fatal("chooser closed while the saved chat was still opening")
	}
	model.sidebarChoice = 2
	second := model.acceptSidebarChooser()
	if second == nil || model.sideOpeningThreadID != "saved-2" {
		t.Fatalf("second open state = %q", model.sideOpeningThreadID)
	}
	firstMsg := first()
	secondMsg := second()
	model.Update(secondMsg)
	if model.State.ThreadID != "saved-2" || model.activeSide == nil || model.activeSide.SideThreadID != "saved-2" {
		t.Fatalf("newest open did not land: thread=%q side=%#v", model.State.ThreadID, model.activeSide)
	}
	model.Update(firstMsg)
	if model.State.ThreadID != "saved-2" || model.activeSide == nil || model.activeSide.SideThreadID != "saved-2" {
		t.Fatalf("stale completion switched threads: thread=%q side=%#v", model.State.ThreadID, model.activeSide)
	}
	if model.sideOpeningThreadID != "" {
		t.Fatalf("opening state survived the completion: %q", model.sideOpeningThreadID)
	}
}

// Rust #60599: Esc cancels the pending attach; its late completion must not
// switch conversations.
func TestSavedChatOpenCancelIgnoresLateCompletionLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.onListSideChats = func(string) ([]SideChatChoice, error) {
		return []SideChatChoice{{ThreadID: "saved-1"}}, nil
	}
	model.onOpenSideChat = func(parentThreadID, sideThreadID string) (AgentThreadSwitchResponse, error) {
		return AgentThreadSwitchResponse{Status: "idle"}, nil
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	model.sidebarChoice = 1
	cmd := model.acceptSidebarChooser()
	if cmd == nil {
		t.Fatal("open returned no command")
	}
	late := cmd()
	model.Update(key(keymsg.KeyEsc))
	if model.sidebarChooserVisible() {
		t.Fatal("Esc did not close the chooser")
	}
	model.Update(late)
	if model.State.ThreadID != "parent-a" || model.activeSide != nil {
		t.Fatalf("cancelled open landed: thread=%q side=%#v", model.State.ThreadID, model.activeSide)
	}
	if model.sideOpeningThreadID != "" {
		t.Fatalf("cancel left opening state %q", model.sideOpeningThreadID)
	}
}
