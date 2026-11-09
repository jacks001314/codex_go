package tea

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	mousemsg "codex_go/tui/mousemsg"
)

// Rust #59928 sidebar_tab_strip.rs: the tab cell is pinned to " Side chat "
// (11 columns), the add button reserves min(width-1, 3) columns before the
// blank last column, and a longer label is clipped at its cell edge instead of
// widening the cell or running into the neighbour.
func TestSidebarTabStripUsesFixedRustTabWidthLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	tabs := make([]*activeSideConversation, 0, 10000)
	for index := 1; index <= 10000; index++ {
		tabs = append(tabs, &activeSideConversation{
			ParentThreadID: "parent-a",
			SideThreadID:   "side-" + strconv.Itoa(index),
		})
	}
	model.sidebarTabs = map[string][]*activeSideConversation{"parent-a": tabs}
	model.activeSide = tabs[len(tabs)-1]
	model.activeSide.ShowingSide = true
	model.refreshTranscript()

	// Rust #60319: count == (available + gap) / (tab_width + gap), so the
	// separating columns are part of the strip budget.
	labels := sidebarTabLabels(len(tabs))
	stride := sidebarTabWidth + sidebarTabGap
	for _, width := range []int{23, 60, 120, 124} {
		available := width - 1 - min(width-1, len(sidebarAddLabel))
		tabWidth, count := sidebarTabLayout(labels, width)
		if want := (available + sidebarTabGap) / stride; count != want {
			t.Fatalf("width %d fits %d tabs, want %d (available %d / %d)",
				width, count, want, available, sidebarTabWidth)
		}
		if available >= sidebarTabWidth && tabWidth != sidebarTabWidth {
			t.Fatalf("width %d tab cell = %d, want the fixed %d columns",
				width, tabWidth, sidebarTabWidth)
		}
	}

	width := model.sidebarTabStripWidth()
	window := model.sidebarTabStripWindowWidth(width)
	available := width - 1 - min(width-1, len(sidebarAddLabel))
	if want := (available + sidebarTabGap) / stride; window.count != want {
		t.Fatalf("strip window fits %d tabs, want %d", window.count, want)
	}
	if window.tabWidth != sidebarTabWidth {
		t.Fatalf("strip tab cell = %d, want %d", window.tabWidth, sidebarTabWidth)
	}
	if window.addStart != available {
		t.Fatalf("strip add button starts at %d, want %d", window.addStart, available)
	}

	strip := ansi.Strip(model.renderSidebarTabStrip(width))
	// The row ends with the add button plus the reserved blank last column.
	if got := ansi.StringWidth(strip); got != width-1 {
		t.Fatalf("strip width = %d, want %d (one reserved column): %q", got, width-1, strip)
	}
	if !strings.HasSuffix(strip, " + ") {
		t.Fatalf("strip does not end with the add button: %q", strip)
	}
	// The visible window keeps the selected tab in view, and its last label is
	// wider than a cell so the clipping path is really exercised.
	firstLabel := " Side " + strconv.Itoa(window.first+1) + " "
	lastLabel := " Side " + strconv.Itoa(window.first+window.count) + " "
	if len(lastLabel) <= window.tabWidth {
		t.Fatalf("test setup: last label %q is not wider than the %d column cell", lastLabel, window.tabWidth)
	}
	for offset := 0; offset < window.count; offset++ {
		label := " Side " + strconv.Itoa(window.first+offset+1) + " "
		want := label
		if len(want) > window.tabWidth {
			want = want[:window.tabWidth]
		} else {
			want += strings.Repeat(" ", window.tabWidth-len(want))
		}
		start := offset * window.stride
		got := strip[start : start+window.tabWidth]
		if got != want {
			t.Fatalf("cell %d = %q, want %q clipped to %d columns", offset, got, want, window.tabWidth)
		}
	}
	if !strings.HasPrefix(strip, firstLabel) {
		t.Fatalf("strip starts with %q, want the first cell %q", strip[:min(len(firstLabel), len(strip))], firstLabel)
	}

	// The add button keeps its column even with the clipped neighbour.
	left, _, _, ok := model.sidePaneLayout(model.width)
	if !ok {
		t.Fatal("split layout unavailable")
	}
	base := left + ansi.StringWidth(sideWorkspaceDivider)
	handled, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
		X: base + window.addStart + 1, Y: model.sidebarTabStripTop,
	})
	if !handled {
		t.Fatalf("press on the add button at column %d was not handled", window.addStart+1)
	}
	runTeaCmd(t, model, cmd)
	if !model.sidebarChooserVisible() {
		t.Fatal("press on the add button did not open the chooser")
	}
}
