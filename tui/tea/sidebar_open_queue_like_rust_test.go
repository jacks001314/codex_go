package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"codex_go/tui/keymsg"
)

func newSidebarQueueModel(t *testing.T, ids ...string) (*Model, *[]string) {
	t.Helper()
	model, _ := newSidebarTabsModel(t)
	choices := make([]SideChatChoice, 0, len(ids))
	for _, id := range ids {
		choices = append(choices, SideChatChoice{ThreadID: id})
	}
	model.onListSideChats = func(string) ([]SideChatChoice, error) { return choices, nil }
	var opened []string
	model.onOpenSideChat = func(parentThreadID, sideThreadID string) (AgentThreadSwitchResponse, error) {
		opened = append(opened, sideThreadID)
		return AgentThreadSwitchResponse{Status: "idle"}, nil
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	return model, &opened
}

// Rust sidebar_loading.rs allows two saved-chat attaches at a time and parks the
// next target in OpenState::Waiting, painting "Waiting to open…" until a flight
// releases its slot (SavedChatReleased -> StartQueuedSavedChat).
func TestSavedChatThirdOpenWaitsForSlotLikeRust(t *testing.T) {
	model, opened := newSidebarQueueModel(t, "saved-1", "saved-2", "saved-3")

	model.sidebarChoice = 1
	first := model.acceptSidebarChooser()
	model.sidebarChoice = 2
	second := model.acceptSidebarChooser()
	model.sidebarChoice = 3
	if third := model.acceptSidebarChooser(); third != nil {
		t.Fatal("third open started instead of waiting for a slot")
	}
	if model.sideOpenState != sideOpenWaiting || model.sideOpeningThreadID != "saved-3" {
		t.Fatalf("third target state = %q for %q", model.sideOpenState, model.sideOpeningThreadID)
	}
	if label := ansi.Strip(model.sideOpenStatusLabel("saved-3")); !strings.Contains(label, "Waiting to open…") {
		t.Fatalf("waiting row label = %q", label)
	}

	firstMsg := first()
	_ = second()
	if len(*opened) != 2 {
		t.Fatalf("attaches before a slot freed = %v", *opened)
	}

	// The superseded flight reports first; its slot frees and the queued target
	// starts on its own.
	_, cmd := model.Update(firstMsg)
	if cmd == nil {
		t.Fatal("releasing a slot did not start the queued target")
	}
	if model.sideOpeningThreadID != "saved-3" || model.sideOpenState != sideOpenLoading {
		t.Fatalf("queued target did not start: state=%q target=%q", model.sideOpenState, model.sideOpeningThreadID)
	}
	_ = cmd()
	if len(*opened) != 3 || (*opened)[2] != "saved-3" {
		t.Fatalf("attaches after a slot freed = %v", *opened)
	}
}

// Accepting the row that is already attaching must not start a second request;
// Rust keeps painting Loading for the same flight.
func TestSavedChatReacceptWhileOpeningDoesNotAttachTwiceLikeRust(t *testing.T) {
	model, opened := newSidebarQueueModel(t, "saved-1")
	model.sidebarChoice = 1
	first := model.acceptSidebarChooser()
	if first == nil {
		t.Fatal("first open returned no command")
	}
	if again := model.acceptSidebarChooser(); again != nil {
		t.Fatal("re-accepting the loading row started a second attach")
	}
	if model.sideOpenState != sideOpenLoading {
		t.Fatalf("state after re-accept = %q", model.sideOpenState)
	}
	_ = first()
	if len(*opened) != 1 {
		t.Fatalf("attaches = %v, want one", *opened)
	}
}

// Cancelling a pending attach retires its flight but keeps its slot until it
// reports; accepting the same chat again waits for that slot, then starts once
// the retired flight finishes (Rust release -> StartQueuedSavedChat).
func TestSavedChatQueuesRetiredTargetLikeRust(t *testing.T) {
	model, opened := newSidebarQueueModel(t, "saved-1")
	model.activeSide = &activeSideConversation{ParentThreadID: "parent-a", SideThreadID: "saved-1"}
	model.sidebarChoice = 1
	first := model.acceptSidebarChooser()
	if first == nil {
		t.Fatal("open returned no command")
	}
	late := first()
	model.Update(key(keymsg.KeyEsc))
	if !model.sidebarChooserVisible() {
		t.Fatal("Esc closed the chooser instead of cancelling the attach")
	}

	if again := model.acceptSidebarChooser(); again != nil {
		t.Fatal("accepting a retired target started a second attach")
	}
	if model.sideOpenState != sideOpenWaiting || model.sideOpeningThreadID != "saved-1" {
		t.Fatalf("retired target state = %q for %q", model.sideOpenState, model.sideOpeningThreadID)
	}

	_, cmd := model.Update(late)
	if cmd == nil {
		t.Fatal("the retired flight did not restart the queued target")
	}
	_ = cmd()
	if len(*opened) != 2 || (*opened)[1] != "saved-1" {
		t.Fatalf("attaches after the retired flight reported = %v", *opened)
	}
	if model.sideOpenState != sideOpenLoading {
		t.Fatalf("queued target state = %q", model.sideOpenState)
	}
}
