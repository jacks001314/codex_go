package tea

import (
	"strings"
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
)

// Rust #62986 workspace.motion: the dock slides in from its last painted width
// with PaneMotion's 180 ms smooth step once a frame requester is attached, and
// the conversation keeps the columns the dock has not painted yet.
func TestAgentsOverviewDockSlideInLikeRust(t *testing.T) {
	model := newAgentsOverviewDockModel(t, 120)
	model.closeAgentsOverview()
	model.frames.attach(func(bubbletea.Msg) {})
	now := time.Unix(4000, 0)
	model.now = func() time.Time { return now }
	model.applyAgentsCommand()
	if model.agentsOverview == nil {
		t.Fatal("applyAgentsCommand did not open the Command Center")
	}

	painted := ansi.Strip(model.viewString())
	if got := model.agentsOverviewDockFraction; got != 0 {
		t.Fatalf("opening frame fraction = %v, want the slide to start at zero", got)
	}
	if !strings.Contains(painted, "hello from the main chat") {
		t.Fatalf("opening frame lost the conversation:\n%s", painted)
	}
	if strings.Contains(painted, "Group:") {
		t.Fatalf("opening frame painted the settled dashboard:\n%s", painted)
	}

	now = now.Add(sidebarSlideDuration / 2)
	painted = ansi.Strip(model.viewString())
	mid := model.agentsOverviewDockFraction
	if mid <= 0 || mid >= 1 {
		t.Fatalf("mid slide fraction = %v, want a value between zero and one", mid)
	}
	// The dock is only a fraction wide here, so the settled dashboard title
	// is clipped; the search row and divider still show the sliding column.
	if !strings.Contains(painted, "Search tasks") || !strings.Contains(painted, "│") ||
		!strings.Contains(painted, "hello from the main chat") {
		t.Fatalf("mid slide lost a pane:\n%s", painted)
	}

	now = now.Add(sidebarSlideDuration)
	_ = model.viewString()
	if got := model.agentsOverviewDockFraction; got != 1 {
		t.Fatalf("settled fraction = %v, want one", got)
	}
	painted = ansi.Strip(model.viewString())
	if !strings.Contains(painted, "Group:") || !strings.Contains(painted, "hello from the main chat") {
		t.Fatalf("settled dock lost a pane:\n%s", painted)
	}
	for _, line := range strings.Split(painted, "\n") {
		if got := ansi.StringWidth(line); got > 120 {
			t.Fatalf("settled row is %d columns wide:\n%s", got, line)
		}
	}
}

// Closing keeps painting the retained dock column while it shrinks, then hands
// the whole viewport back to the conversation.
func TestAgentsOverviewDockSlideOutLikeRust(t *testing.T) {
	model := newAgentsOverviewDockModel(t, 120)
	model.frames.attach(func(bubbletea.Msg) {})
	now := time.Unix(5000, 0)
	model.now = func() time.Time { return now }
	_ = model.viewString()
	now = now.Add(sidebarSlideDuration)
	_ = model.viewString()
	if model.agentsOverviewDockFraction != 1 {
		t.Fatalf("fraction = %v before closing, want one", model.agentsOverviewDockFraction)
	}
	settled, _, ok := model.agentsOverviewDockWidths()
	if !ok {
		t.Fatal("no settled dock layout")
	}

	model.closeAgentsOverview()
	if model.agentsOverview != nil {
		t.Fatal("closeAgentsOverview kept the view")
	}
	if model.agentsOverviewClosingWidth != settled {
		t.Fatalf("closing snapshot width = %d, want %d", model.agentsOverviewClosingWidth, settled)
	}
	painted := ansi.Strip(model.viewString())
	if got := model.agentsOverviewDockFraction; got != 1 {
		t.Fatalf("first closing frame fraction = %v, want the settled width", got)
	}
	if !strings.Contains(painted, "Group:") {
		t.Fatalf("closing frame dropped the dock immediately:\n%s", painted)
	}

	now = now.Add(sidebarSlideDuration / 2)
	painted = ansi.Strip(model.viewString())
	mid := model.agentsOverviewDockFraction
	if mid <= 0 || mid >= 1 {
		t.Fatalf("mid close fraction = %v, want a value between zero and one", mid)
	}
	if !strings.Contains(painted, "hello from the main chat") {
		t.Fatalf("closing frame lost the conversation:\n%s", painted)
	}

	now = now.Add(sidebarSlideDuration)
	painted = ansi.Strip(model.viewString())
	if model.agentsOverviewClosingLines != nil {
		t.Fatal("closing snapshot was not released")
	}
	if strings.Contains(painted, "Group:") {
		t.Fatalf("settled close still painted the dock:\n%s", painted)
	}
	if !strings.Contains(painted, "hello from the main chat") {
		t.Fatalf("settled close lost the conversation:\n%s", painted)
	}
}

// Without a frame requester the dock snaps, like PaneMotion::advance with no
// requester, and a snapped close retains nothing.
func TestAgentsOverviewDockMotionSnapsWithoutRequesterLikeRust(t *testing.T) {
	model := newAgentsOverviewDockModel(t, 120)
	_ = model.viewString()
	if got := model.agentsOverviewDockFraction; got != 1 {
		t.Fatalf("fraction = %v, want an immediate snap", got)
	}
	model.closeAgentsOverview()
	if model.agentsOverviewClosingLines != nil {
		t.Fatal("snapping close retained a closing column")
	}
	painted := ansi.Strip(model.viewString())
	if strings.Contains(painted, "Group:") {
		t.Fatalf("snapped close still painted the dock:\n%s", painted)
	}
}
