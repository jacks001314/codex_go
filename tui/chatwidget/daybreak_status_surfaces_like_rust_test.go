package chatwidget

import (
	"testing"

	bottompane "codex_go/tui/bottom_pane"
)

// TestDaybreakStatusSurfacesFollowThreadPreferenceLikeRust mirrors Rust #49861
// (chatwidget/tests/terminal_title.rs::daybreak_status_surfaces_follow_the_thread_preference):
// the status line and terminal title report "Daybreak on" only while the thread
// has Daybreak enabled and no side conversation owns the composer.
func TestDaybreakStatusSurfacesFollowThreadPreferenceLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		daybreak bool
		side     bool
		want     string
	}{
		{name: "disabled thread reports off", daybreak: false, want: "Daybreak off"},
		{name: "enabled thread reports on", daybreak: true, want: "Daybreak on"},
		{name: "side conversation reports off", daybreak: true, side: true, want: "Daybreak off"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := NewStatusControlsState(StatusControlsRuntime{
				DaybreakEnabled:        tc.daybreak,
				SideConversationActive: tc.side,
			})
			got, ok := state.StatusLineValueForItem(bottompane.StatusLineDaybreak)
			if !ok || got != tc.want {
				t.Fatalf("status line daybreak = %q ok=%v, want %q ok=true", got, ok, tc.want)
			}
			title, ok := state.TerminalTitleValueForItem(TerminalTitleDaybreak)
			if !ok || title != tc.want {
				t.Fatalf("terminal title daybreak = %q ok=%v, want %q ok=true", title, ok, tc.want)
			}
		})
	}
}

// TestDaybreakItemIsSelectableLikeRust covers the setup surfaces Rust #49861
// adds for the new item: the status line and terminal title parsers accept
// "daybreak", the setup lists expose it between fast-mode and raw-output, and
// the preview placeholder is "Daybreak off".
func TestDaybreakItemIsSelectableLikeRust(t *testing.T) {
	lineItem, ok := ParseStatusLineItem("daybreak")
	if !ok || lineItem != bottompane.StatusLineDaybreak {
		t.Fatalf("ParseStatusLineItem(daybreak) = %v ok=%v", lineItem, ok)
	}
	if id := StatusLineItemID(bottompane.StatusLineDaybreak); id != "daybreak" {
		t.Fatalf("StatusLineItemID(daybreak) = %q", id)
	}
	titleItem, ok := ParseTerminalTitleItem("daybreak")
	if !ok || titleItem != TerminalTitleDaybreak {
		t.Fatalf("ParseTerminalTitleItem(daybreak) = %v ok=%v", titleItem, ok)
	}
	if got := TerminalTitleItemDescription(TerminalTitleDaybreak, DefaultStatusSurfacePreviewData()); got != "Whether Daybreak is enabled for this thread" {
		t.Fatalf("terminal title description = %q", got)
	}
	if got := StatusLineItemDescription(bottompane.StatusLineDaybreak, DefaultStatusSurfacePreviewData()); got != "Whether Daybreak is enabled for this thread" {
		t.Fatalf("status line description = %q", got)
	}
	preview, ok := DefaultStatusSurfacePreviewData().ValueFor(StatusPreviewDaybreak)
	if !ok || preview != "Daybreak off" {
		t.Fatalf("daybreak preview = %q ok=%v, want Daybreak off", preview, ok)
	}
	// The item sits right after fast-mode in both setup lists, like Rust's
	// declaration order.
	if !orderedRightAfter(StatusLineItemIDs(AllStatusLineItems()), "fast-mode", "daybreak") {
		t.Fatalf("status line setup order = %v", StatusLineItemIDs(AllStatusLineItems()))
	}
	titleIDs := make([]string, 0, len(AllTerminalTitleItems()))
	for _, item := range AllTerminalTitleItems() {
		titleIDs = append(titleIDs, item.ID())
	}
	if !orderedRightAfter(titleIDs, "fast-mode", "daybreak") {
		t.Fatalf("terminal title setup order = %v", titleIDs)
	}
	view := NewStatusLineSetupView([]bottompane.StatusLineItem{bottompane.StatusLineDaybreak}, true, DefaultStatusSurfacePreviewData())
	found := false
	for _, item := range view.Items {
		if item.ID == "daybreak" {
			found = item.Selected && item.Preview == "Daybreak off"
		}
	}
	if !found {
		t.Fatalf("status line setup view omitted daybreak: %#v", view.Items)
	}
}

func orderedRightAfter(ids []string, before string, after string) bool {
	for i, id := range ids {
		if id == before {
			return i+1 < len(ids) && ids[i+1] == after
		}
	}
	return false
}
