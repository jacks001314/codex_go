package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// TestSlashPopupNameColumnStaysPutWhileScrollingLikeRust pins Rust's
// ColumnWidthMode::AutoAllRows (bottom_pane/command_popup.rs:27-31): the name
// column is sized from the whole filtered list, so scrolling the popup never
// shifts the description column.
// TestPopupWindowStaysPutWhenScrollingBackLikeRust pins Rust's ScrollState
// window (bottom_pane/scroll_state.rs + selection_popup_common.rs:572-586): once
// the viewport has reached the end of a long list, moving the selection back up
// inside it keeps scroll_top, instead of pinning the selection to the last row.
func TestPopupWindowStaysPutWhenScrollingBackLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	typeText(t, model, "/")
	for range len(model.slashPopup.Items) - 1 {
		model.moveSlashPopupSelection(1)
	}
	bottom := model.slashPopup.ScrollTop
	if want := len(model.slashPopup.Items) - slashPopupMaxRows; bottom != want {
		t.Fatalf("scroll_top at the end = %d, want %d", bottom, want)
	}
	model.moveSlashPopupSelection(-1)
	model.moveSlashPopupSelection(-1)
	if got := model.slashPopup.ScrollTop; got != bottom {
		t.Fatalf("scroll_top after stepping back = %d, want %d", got, bottom)
	}
	visible := popupVisibleRange(len(model.slashPopup.Items), model.slashPopup.Selected, model.slashPopup.ScrollTop, slashPopupMaxRows)
	if visible.start != bottom {
		t.Fatalf("visible window moved to %d, want %d", visible.start, bottom)
	}
	for range slashPopupMaxRows {
		model.moveSlashPopupSelection(-1)
	}
	// The window only moves once the selection leaves it through the top row.
	want := bottom
	if selected := model.slashPopup.Selected; selected < bottom {
		want = selected
	}
	if got := model.slashPopup.ScrollTop; got != want {
		t.Fatalf("scroll_top after leaving the window = %d, want %d", got, want)
	}
}

func TestSlashPopupNameColumnStaysPutWhileScrollingLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	typeText(t, model, "/")
	longest := 0
	for _, item := range model.slashPopup.Items {
		if width := codextui.DisplayWidth(slashPopupDisplayName(item)); width > longest {
			longest = width
		}
	}
	if longest <= codextui.DisplayWidth("/experimental") {
		t.Fatalf("popup has no long command name to pin the column: longest = %d", longest)
	}
	want := 2 + longest + 2
	descriptionColumn := func(needle string) int {
		t.Helper()
		for _, line := range strings.Split(utils.StripANSI(model.renderSlashPopup()), "\n") {
			// The selected row's marker is a multi-byte rune; normalize it so
			// every row is measured from the same column.
			line = strings.Replace(line, "›", " ", 1)
			if idx := strings.Index(line, needle); idx >= 0 {
				return idx
			}
		}
		t.Fatalf("popup missing %q:\n%s", needle, utils.StripANSI(model.renderSlashPopup()))
		return -1
	}
	if got := descriptionColumn("choose what model and reasoning effort"); got != want {
		t.Fatalf("first page description column = %d, want %d", got, want)
	}
	for _, step := range []int{8, 16, 24, 32} {
		model.slashPopup.Selected = step
		visible := popupVisibleRange(len(model.slashPopup.Items), step, model.slashPopup.ScrollTop, slashPopupMaxRows)
		description := slashPopupDisplayDescription(model.slashPopup.Items[visible.start])
		if description == "" {
			t.Fatalf("selected row %d has no description to measure", step)
		}
		if got := descriptionColumn(description); got != want {
			t.Fatalf("row %d description column = %d, want %d (the column must not shift while scrolling)", step, got, want)
		}
	}
}

// TestPopupKeepsViewWithinTerminalHeight guards against the slash/skill popup
// overflowing the terminal. The animation engine repaints at 20 FPS, so a view
// taller than the terminal makes it auto-scroll continuously.
func TestPopupKeepsViewWithinTerminalHeight(t *testing.T) {
	for _, tc := range []struct{ w, h int }{
		{61, 32},
		{120, 30},
		{80, 24},
	} {
		model := NewModel(codextui.NewState(nil), Options{Width: tc.w, Height: tc.h})
		typeText(t, model, "/")
		view := model.View()
		lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
		if len(lines) > tc.h {
			t.Fatalf("size %dx%d: slash popup view = %d lines, exceeds terminal height %d", tc.w, tc.h, len(lines), tc.h)
		}
		if !strings.Contains(view, codextui.SelectionPrefix(true)+"/model") {
			t.Fatalf("size %dx%d: slash popup missing from view:\n%s", tc.w, tc.h, view)
		}
		// Closing the popup must restore the full transcript height.
		model.Update(key(bubbletea.KeyEsc))
		if model.slashPopup.Active {
			t.Fatalf("size %dx%d: slash popup still active after Esc", tc.w, tc.h)
		}
		if model.View() == view {
			t.Fatalf("size %dx%d: view unchanged after closing popup", tc.w, tc.h)
		}
	}
}
