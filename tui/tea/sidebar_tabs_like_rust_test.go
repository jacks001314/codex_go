package tea

import (
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

func newSidebarTabsModel(t *testing.T) (*Model, *[]SideStartParams) {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("parent-a")
	var started []SideStartParams
	model := NewModel(state, Options{
		Width:  120,
		Height: 30,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			started = append(started, params)
			return SideStartResponse{
				ParentThreadID: params.ParentThreadID,
				SideThreadID:   "side-" + string(rune('1'+len(started)-1)),
			}, nil
		},
		OnSubmit: func(string) bubbletea.Cmd { return nil },
	})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.refreshTranscript()
	return model, &started
}

// Rust #59739: with no side chat yet, the sidebar shortcut opens a chooser
// listing "New side chat" instead of doing nothing.
func TestSidebarChooserOffersNewSideChatLikeRust(t *testing.T) {
	model, started := newSidebarTabsModel(t)

	if cmd := model.focusSidebarContent(); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if !model.sidebarChooserVisible() || !model.sidebarChooserFocused {
		t.Fatalf("chooser did not open: open=%t focused=%t", model.sidebarChooserOpen, model.sidebarChooserFocused)
	}
	if left, right, sideFocused, ok := model.sidePaneLayout(120); !ok || !sideFocused || left <= 0 || right <= 0 {
		t.Fatalf("chooser layout = %d/%d focused=%t ok=%t", left, right, sideFocused, ok)
	}
	rendered := model.renderSidebarChooser(40, 20)
	for _, want := range []string{sidebarChooserTitle, sidebarNewChatTitle, "Esc back"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("chooser missing %q:\n%s", want, rendered)
		}
	}

	_, cmd := model.Update(key(keymsg.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(*started) != 1 {
		t.Fatalf("side starters = %d, want 1", len(*started))
	}
	if model.activeSide == nil || model.State.ThreadID != "side-1" {
		t.Fatalf("active side = %#v thread = %q", model.activeSide, model.State.ThreadID)
	}
	if model.sidebarChooserVisible() {
		t.Fatal("chooser stayed open after starting a side chat")
	}
}

// Rust #59784: a side chat opened with a prompt is a new sibling while a bare
// /side reopens the selected one, and the tab strip numbers the siblings.
func TestSidebarTabsKeepMultipleSideChatsLikeRust(t *testing.T) {
	model, started := newSidebarTabsModel(t)

	openSideForTest(t, model)
	if len(*started) != 1 {
		t.Fatalf("side starters = %d, want 1", len(*started))
	}
	if cmd := model.toggleSideConversation(); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	typeText(t, model, "/side another question")
	_, cmd := model.Update(key(keymsg.KeyEnter))
	runTeaCmd(t, model, cmd)

	if len(*started) != 2 {
		t.Fatalf("side starters = %d, want a new sibling for /side <prompt>", len(*started))
	}
	tabs := model.sidebarTabsFor("parent-a")
	if len(tabs) != 2 {
		t.Fatalf("sidebar tabs = %d, want 2", len(tabs))
	}
	if tabs[0].SideThreadID != "side-1" || tabs[1].SideThreadID != "side-2" {
		t.Fatalf("tab order = %q,%q", tabs[0].SideThreadID, tabs[1].SideThreadID)
	}
	strip := model.renderSidebarTabStrip(60)
	for _, want := range []string{" Side 1 ", " Side 2 ", sidebarAddLabel} {
		if !strings.Contains(strip, want) {
			t.Fatalf("tab strip missing %q: %q", want, strip)
		}
	}

	// A bare /side reopens the selected sibling instead of forking again.
	if cmd := model.toggleSideConversation(); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	openSideForTest(t, model)
	if len(*started) != 2 {
		t.Fatalf("bare /side forked another chat: starters = %d", len(*started))
	}

	// The chooser reopens the older sibling.
	if cmd := model.toggleSideConversation(); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	model.sidebarChoice = 0
	_, cmd = model.Update(key(keymsg.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.State.ThreadID != "side-1" {
		t.Fatalf("chooser reopened %q, want side-1", model.State.ThreadID)
	}
}

// Rust #59824: the chooser shares the sidebar split, so it can be resized, and
// finishing a resize with Enter must not close it or submit the parent draft.
func TestSidebarChooserResizesLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if cmd := model.beginSidePaneResize(); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if model.sideResize == nil {
		t.Fatal("chooser did not start a resize preview")
	}
	preview := model.sideResize.previewWidth
	model.Update(key(keymsg.KeyLeft))
	if model.sideResize == nil || model.sideResize.previewWidth <= preview {
		t.Fatalf("left arrow did not widen the chooser preview: %#v", model.sideResize)
	}
	if handled, cmd := model.handleSidePaneResizeKey(key(keymsg.KeyEnter)); !handled {
		t.Fatal("Enter did not finish the resize")
	} else if cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if model.sideResize != nil {
		t.Fatal("resize preview survived Enter")
	}
	if !model.sidebarChooserVisible() {
		t.Fatal("finishing the resize closed the chooser")
	}
	if model.sideWidthPercent <= 0 {
		t.Fatalf("sidebar width percent = %d", model.sideWidthPercent)
	}
}
