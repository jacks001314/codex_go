package tea

import (
	"strings"
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
)

// Rust #59739 PaneMotion: the sidebar slides in from its last painted width
// with a smooth-step over 180 ms once a frame requester is attached.
func TestSidebarSlideAnimationLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	model.frames.attach(func(bubbletea.Msg) {})
	now := time.Unix(1000, 0)
	model.now = func() time.Time { return now }
	if _, ok := model.renderSideWorkspace(120, 20); ok {
		t.Fatal("split painted without a side pane")
	}
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	settledLeft, settledRight, _, ok := model.sidePaneLayout(120)
	if !ok {
		t.Fatal("no side pane layout after opening the reader")
	}

	_, ok = model.renderSideWorkspace(120, 20)
	if !ok {
		t.Fatal("opening frame painted no split")
	}
	startColumns := sidebarDividerColumn(t, model)
	if startColumns <= settledLeft {
		t.Fatalf("opening frame divider at %d, want the parent still wide before the slide (settled left %d)", startColumns, settledLeft)
	}
	if model.sidebarClosingWidth != settledRight {
		t.Fatalf("settled right column snapshot = %d, want %d", model.sidebarClosingWidth, settledRight)
	}

	now = now.Add(sidebarSlideDuration / 2)
	model.renderSideWorkspace(120, 20)
	midColumns := sidebarDividerColumn(t, model)
	if midColumns >= startColumns || midColumns <= settledLeft {
		t.Fatalf("mid slide divider at %d, want between %d and %d", midColumns, settledLeft, startColumns)
	}

	now = now.Add(sidebarSlideDuration)
	model.renderSideWorkspace(120, 20)
	if got := sidebarDividerColumn(t, model); got != settledLeft {
		t.Fatalf("settled divider at %d, want %d", got, settledLeft)
	}
	if model.paneMotion.fraction != 1 {
		t.Fatalf("fraction after settling = %v, want 1", model.paneMotion.fraction)
	}
}

// Closing keeps painting the last settled column while it shrinks, then hands
// the frame back to the single conversation.
func TestSidebarCloseSlideLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	model.frames.attach(func(bubbletea.Msg) {})
	now := time.Unix(2000, 0)
	model.now = func() time.Time { return now }
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	model.renderSideWorkspace(120, 20)
	now = now.Add(sidebarSlideDuration)
	model.renderSideWorkspace(120, 20)
	settledLeft, _, _, ok := model.sidePaneLayout(120)
	if !ok {
		t.Fatal("no settled layout")
	}

	side := model.activeSide
	runTeaCmd(t, model, model.executeSidebarTabClose(side))
	if model.activeSide != nil {
		t.Fatalf("reader survived the close: %#v", model.activeSide)
	}
	if _, ok := model.renderSideWorkspace(120, 20); !ok {
		t.Fatal("closing slide stopped painting the moment the pane closed")
	}
	now = now.Add(sidebarSlideDuration / 2)
	model.renderSideWorkspace(120, 20)
	if got := sidebarDividerColumn(t, model); got <= settledLeft {
		t.Fatalf("mid close divider at %d, want the parent wider than %d while closing", got, settledLeft)
	}
	now = now.Add(sidebarSlideDuration)
	if _, ok := model.renderSideWorkspace(120, 20); ok {
		t.Fatal("closing slide never returned to the single conversation")
	}
	if model.sidebarClosingLines != nil {
		t.Fatal("retained closing column was not released")
	}
}

// Without a frame requester the motion snaps, like PaneMotion::advance with no
// requester.
func TestSidebarMotionSnapsWithoutRequesterLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	now := time.Unix(3000, 0)
	model.now = func() time.Time { return now }
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	model.renderSideWorkspace(120, 20)
	if model.paneMotion.fraction != 1 {
		t.Fatalf("fraction = %v, want an immediate snap", model.paneMotion.fraction)
	}
	settledLeft, _, _, _ := model.sidePaneLayout(120)
	if got := sidebarDividerColumn(t, model); got != settledLeft {
		t.Fatalf("divider at %d, want the settled %d", got, settledLeft)
	}
}

// sidebarDividerColumn reports the column of the pane divider in the last
// rendered workspace frame.
func sidebarDividerColumn(t *testing.T, model *Model) int {
	t.Helper()
	rendered, ok := model.renderSideWorkspace(120, 20)
	if !ok {
		t.Fatal("no side workspace frame")
	}
	line := strings.Split(rendered, "\n")[0]
	column := strings.Index(ansi.Strip(line), sideWorkspaceDivider)
	if column < 0 {
		t.Fatalf("no divider in frame row %q", ansi.Strip(line))
	}
	return column
}
