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
// blank last column, a longer label is clipped at its cell edge instead of
// widening the cell or running into the neighbour, and the last column of the
// cell is the close glyph (sidebar_chooser::close_cell).
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

	// Rust TabStrip::strip_targets: count == (space + gap) / (tab_width + gap)
	// where space drops both overflow gutters, so the separating columns are
	// part of the strip budget.
	labels := sidebarTabLabels(len(tabs), false)
	stride := sidebarTabWidth + sidebarTabGap
	for _, width := range []int{23, 60, 120, 124} {
		available := width - 1 - min(width-1, len(sidebarAddLabel))
		overflow := len(labels) > max((available+sidebarTabGap)/stride, 1)
		gutter := 0
		if overflow && available >= 7 {
			gutter = sidebarTabOverflowGutter
		}
		space := available - 2*gutter
		wantWidth := min(sidebarTabWidth, space)
		wantCount := (space + sidebarTabGap) / (wantWidth + sidebarTabGap)
		if wantCount > 0 {
			// Rust #63189 spends spare space on the wider close target.
			roomPerTab := (space+sidebarTabGap)/wantCount - sidebarTabGap
			wantWidth = min(wantWidth+sidebarTabCloseWidth-1, roomPerTab)
		}
		tabWidth, count := sidebarTabLayout(labels, width)
		if count != wantCount {
			t.Fatalf("width %d fits %d tabs, want %d (space %d / %d)",
				width, count, wantCount, space, wantWidth)
		}
		if tabWidth != wantWidth {
			t.Fatalf("width %d tab cell = %d, want %d", width, tabWidth, wantWidth)
		}
	}

	width := model.sidebarTabStripWidth()
	window := model.sidebarTabStripWindowWidth(width)
	available := width - 1 - min(width-1, len(sidebarAddLabel))
	space := available - 2*window.gutter
	if want := (space + sidebarTabGap) / (window.tabWidth + sidebarTabGap); window.count != want {
		t.Fatalf("strip window fits %d tabs, want %d", window.count, want)
	}
	if window.gutter != sidebarTabOverflowGutter || !window.leftArrow {
		t.Fatalf("overflowing strip gutter = %d left=%t, want %d/true", window.gutter, window.leftArrow, sidebarTabOverflowGutter)
	}
	if want := window.first+window.count < len(tabs); window.rightArrow != want {
		t.Fatalf("trailing arrow = %t, want %t", window.rightArrow, want)
	}
	wantWidth := min(sidebarTabWidth, space)
	if window.count > 0 {
		// Rust #63189 widens each cell by the spare columns the close target needs.
		roomPerTab := (space+sidebarTabGap)/window.count - sidebarTabGap
		wantWidth = min(wantWidth+sidebarTabCloseWidth-1, roomPerTab)
	}
	if window.tabWidth != wantWidth {
		t.Fatalf("strip tab cell = %d, want %d", window.tabWidth, wantWidth)
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
	if labelWidth := window.tabWidth - sidebarTabCloseWidth; len(lastLabel) <= labelWidth {
		t.Fatalf("test setup: last label %q is not wider than the %d column label area", lastLabel, labelWidth)
	}
	for offset := 0; offset < window.count; offset++ {
		label := " Side " + strconv.Itoa(window.first+offset+1) + " "
		want := label
		labelWidth := window.tabWidth - sidebarTabCloseWidth
		if len(want) > labelWidth {
			want = want[:labelWidth]
		} else {
			want += strings.Repeat(" ", labelWidth-len(want))
		}
		want += sidebarTabCloseCell
		start := window.gutter + offset*window.stride
		got := ansi.Cut(strip, start, start+window.tabWidth)
		if got != want {
			t.Fatalf("cell %d = %q, want %q clipped to %d columns", offset, got, want, window.tabWidth)
		}
	}
	firstTitle := firstLabel
	if len(firstTitle) > window.tabWidth-sidebarTabCloseWidth {
		firstTitle = firstTitle[:window.tabWidth-sidebarTabCloseWidth]
	}
	if !strings.HasPrefix(strip, sidebarTabOverflowLeft+strings.Repeat(" ", window.gutter-1)+firstTitle) {
		t.Fatalf("strip starts with %q, want the first cell %q", strip[:min(len(firstLabel), len(strip))], firstLabel)
	}

	// The add button keeps its column even with the clipped neighbour.
	left, _, _, ok := model.sidePaneLayout(model.width)
	if !ok {
		t.Fatal("split layout unavailable")
	}
	base := left + ansi.StringWidth(sideWorkspaceDivider)
	handled, cmd := clickSidebarPane(t, model, mousemsg.MouseButtonLeft, base+window.addStart+1, model.sidebarTabStripTop)
	if !handled {
		t.Fatalf("press on the add button at column %d was not handled", window.addStart+1)
	}
	runTeaCmd(t, model, cmd)
	if !model.sidebarChooserVisible() {
		t.Fatal("press on the add button did not open the chooser")
	}
}
