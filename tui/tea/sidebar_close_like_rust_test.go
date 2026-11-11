package tea

import (
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// TestCloseSidebarTabLeaderBindingLikeRust covers Rust #60152: the action is
// catalogued and installed as the implicit `leader w` chord.
func TestCloseSidebarTabLeaderBindingLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 30})
	actions, _, ok := model.leaderBindingsFor("ctrl-x", nil)
	if !ok {
		t.Fatal("default leader prefix is missing")
	}
	if got := actions["w"]; got != "global.close_sidebar_tab" {
		t.Fatalf("leader w = %q, want global.close_sidebar_tab (bindings=%#v)", got, actions)
	}
	if _, ok := codextui.FindKeymapAction("global", "close_sidebar_tab"); !ok {
		t.Fatal("close_sidebar_tab is missing from the keymap catalog")
	}
	config := codextui.NewKeymapConfig()
	if err := config.Set("global", "close_sidebar_tab", []string{"ctrl-q"}); err != nil {
		t.Fatalf("close_sidebar_tab is not a config target: %v", err)
	}
}

// TestCloseSidebarTabAsksBeforeLosingWorkLikeRust mirrors the Rust
// confirmation: unsent input or retained history needs an explicit Close, and
// cancelling keeps the tab.
func TestCloseSidebarTabAsksBeforeLosingWorkLikeRust(t *testing.T) {
	model, side := newWorkspacePaneModel(t)
	model.registerSidebarTab(side)
	side.SideMessages = []codextui.Message{{Role: codextui.RoleUser, Text: "draft a plan"}}
	typeText(t, model, "unsent draft")

	model.closeSidebarTab()
	if model.modal == nil || model.modal.id != sideTabCloseModalID {
		t.Fatalf("close without confirming; modal=%#v", model.modal)
	}
	if model.pendingSideTabCloseThreadID != "side" {
		t.Fatalf("pending close target = %q", model.pendingSideTabCloseThreadID)
	}
	model.activateModalOption(0)
	if model.pendingSideTabCloseThreadID != "" {
		t.Fatalf("cancel kept the pending target: %q", model.pendingSideTabCloseThreadID)
	}
	if model.activeSide != side || model.State.ThreadID != "side" {
		t.Fatalf("cancel closed the tab; side=%#v thread=%q", model.activeSide, model.State.ThreadID)
	}

	model.closeSidebarTab()
	model.activateModalOption(1)
	if model.modal != nil {
		t.Fatal("confirmation survived the close")
	}
	if model.activeSide != nil || model.State.ThreadID != "parent" {
		t.Fatalf("close left side=%#v thread=%q", model.activeSide, model.State.ThreadID)
	}
	if tab := model.sidebarTabByThreadID("side"); tab != nil {
		t.Fatalf("closed tab is still registered: %#v", tab)
	}
}

// TestCloseSidebarTabClosesIdleTabLikeRust asserts an idle tab closes without
// a dialog, and the leader chord drives the same flow.
func TestCloseSidebarTabClosesIdleTabLikeRust(t *testing.T) {
	model, side := newWorkspacePaneModel(t)
	model.registerSidebarTab(side)
	model.closeSidebarTab()
	if model.modal != nil {
		t.Fatalf("idle tab asked for confirmation: %#v", model.modal)
	}
	if model.activeSide != nil || model.State.ThreadID != "parent" {
		t.Fatalf("idle close left side=%#v thread=%q", model.activeSide, model.State.ThreadID)
	}

	chord, chordSide := newWorkspacePaneModel(t)
	chord.registerSidebarTab(chordSide)
	chord.Update(key(keymsg.KeyCtrlX))
	chord.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'w'}})
	if chord.leaderChordPending {
		t.Fatal("leader chord stayed pending after w")
	}
	if chord.activeSide != nil || chord.State.ThreadID != "parent" {
		t.Fatalf("leader w left side=%#v thread=%q", chord.activeSide, chord.State.ThreadID)
	}
}
