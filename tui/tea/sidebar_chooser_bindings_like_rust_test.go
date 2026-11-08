package tea

import (
	"strings"
	"testing"

	"codex_go/tui/keymsg"
)

// Mirrors Rust #59928: the sidebar chooser drives the shared `list` keymap
// context instead of its own Up/Down/Esc/Enter switch, and the tab strip paints
// the tab under the pointer.
func TestSidebarChooserUsesListBindingsAndHoverLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.registerSidebarTab(&activeSideConversation{ParentThreadID: "parent-a", SideThreadID: "side-1"})
	model.registerSidebarTab(&activeSideConversation{ParentThreadID: "parent-a", SideThreadID: "side-2"})
	if cmd := model.focusSidebarContent(); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if !model.sidebarChooserVisible() {
		t.Fatal("chooser did not open")
	}

	// The chooser opens on the trailing "+ New side chat" row; jump_home moves to
	// the first tab, then ctrl-n / ctrl-p are the shared list move_down / move_up
	// bindings. The old hardcoded chooser only accepted the arrow keys.
	if handled, _ := model.updateSidebarChooserKey(key(keymsg.KeyHome)); !handled || model.sidebarChoice != 0 {
		t.Fatalf("home handled=%t choice=%d, want the first row", handled, model.sidebarChoice)
	}
	if handled, _ := model.updateSidebarChooserKey(key(keymsg.KeyCtrlN)); !handled || model.sidebarChoice != 1 {
		t.Fatalf("ctrl-n handled=%t choice=%d, want the list move_down binding", handled, model.sidebarChoice)
	}
	if handled, _ := model.updateSidebarChooserKey(key(keymsg.KeyCtrlP)); !handled || model.sidebarChoice != 0 {
		t.Fatalf("ctrl-p handled=%t choice=%d, want the list move_up binding", handled, model.sidebarChoice)
	}
	// jump_bottom / jump_top are also shared list bindings.
	if handled, _ := model.updateSidebarChooserKey(key(keymsg.KeyEnd)); !handled || model.sidebarChoice != 2 {
		t.Fatalf("end handled=%t choice=%d, want the last row (the new-chat entry)", handled, model.sidebarChoice)
	}
	// accept / cancel still resolve through the same context.
	if action := sidebarChooserListAction(key(keymsg.KeyEnter)); action != "accept" {
		t.Fatalf("enter resolves to %q, want accept", action)
	}
	if action := sidebarChooserListAction(key(keymsg.KeyEsc)); action != "cancel" {
		t.Fatalf("esc resolves to %q, want cancel", action)
	}
	if action := sidebarChooserListAction(key(keymsg.KeyCtrlN)); action != "move_down" {
		t.Fatalf("ctrl-n resolves to %q, want move_down", action)
	}

	// Hover: the strip maps columns back to tabs and paints the hovered one.
	tabs := model.sidebarTabsFor(model.sidebarChooserParent)
	tabWidth, count := sidebarTabLayout(sidebarTabLabels(len(tabs)), 120)
	if count != 2 {
		t.Fatalf("strip fits %d tabs in 120 columns, want 2", count)
	}
	if index := sidebarTabHoverIndex(tabs, tabWidth, 120); index != 1 {
		t.Fatalf("hover column %d = %d, want tab 1", tabWidth, index)
	}
	if index := sidebarTabHoverIndex(tabs, count*tabWidth, 120); index != -1 {
		t.Fatalf("hover past the last tab = %d, want -1", index)
	}
	model.sidebarTabHover = -1
	if changed := model.hoverSidebarTab(tabWidth); !changed || model.sidebarTabHover != 1 {
		t.Fatalf("hoverSidebarTab() changed=%t hover=%d, want tab 1", changed, model.sidebarTabHover)
	}
	strip := model.renderSidebarTabStrip(120)
	if strings.Count(strip, "\x1b[7m") != 1 {
		t.Fatalf("strip painted %d hovered cells, want exactly one:\n%q", strings.Count(strip, "\x1b[7m"), strip)
	}
	if !strings.Contains(strip, "\x1b[7m Side 2 ") {
		t.Fatalf("strip did not hover-highlight the second tab:\n%q", strip)
	}
	if changed := model.hoverSidebarTab(count * tabWidth); !changed || model.sidebarTabHover != -1 {
		t.Fatalf("hover over the + button changed=%t hover=%d, want no tab", changed, model.sidebarTabHover)
	}
	if strip := model.renderSidebarTabStrip(120); strings.Contains(strip, "\x1b[7m") {
		t.Fatalf("strip kept a hover highlight past the tabs:\n%q", strip)
	}
}
