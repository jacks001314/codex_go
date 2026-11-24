package tea

import (
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
)

// Rust split_layout measures the side conversation against workspace_chat_area,
// so with the Command Center docked a side pane splits the conversation column
// instead of the whole terminal (#62986 side_pane.rs).
func TestAgentsOverviewDockSplitsSideConversationLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	model.ownedScreen = true
	model.Update(bubbletea.WindowSizeMsg{Width: 160, Height: 30})
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	if !model.sideWorkspaceOpen() {
		t.Fatal("side conversation did not open")
	}
	_ = model.viewString()
	model.applyAgentsCommand()
	if !model.agentsOverviewDockOpen() {
		t.Fatalf("dock did not open at %d columns", model.width)
	}
	conversationWidth := 160 - agentsOverviewDockCenterWidth - agentsOverviewDockGutter
	if conversationWidth < sideWorkspaceMinWidth {
		t.Fatalf("conversation column = %d, want room for the side split", conversationWidth)
	}

	painted := ansi.Strip(model.viewString())
	for _, want := range []string{"Group:", "hello from the main chat", "agent transcript for agent-1"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("docked frame lost %q:\n%s", want, painted)
		}
	}
	for _, line := range strings.Split(painted, "\n") {
		if got := ansi.StringWidth(line); got > 160 {
			t.Fatalf("row is %d columns wide:\n%s", got, line)
		}
	}

	// The side divider sits inside the conversation column, right of the dock.
	leftWidth, _, _, ok := model.sidePaneLayout(conversationWidth)
	if !ok {
		t.Fatal("no side split layout inside the conversation column")
	}
	row := []rune(strings.Split(painted, "\n")[0])
	innerDivider := agentsOverviewDockCenterWidth + agentsOverviewDockGutter + leftWidth
	if len(row) <= innerDivider || row[innerDivider] != []rune(sideWorkspaceDivider)[0] {
		t.Fatalf("no side divider at column %d:\n%s", innerDivider, string(row))
	}
}

// A conversation column too narrow for the side split keeps the plain dock.
func TestAgentsOverviewDockKeepsPlainColumnWhenSideSplitCannotFit(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	model.ownedScreen = true
	model.Update(bubbletea.WindowSizeMsg{Width: 120, Height: 30})
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	if !model.sideWorkspaceOpen() {
		t.Fatal("side conversation did not open")
	}
	model.applyAgentsCommand()
	if !model.agentsOverviewDockOpen() {
		t.Fatalf("dock did not open at %d columns", model.width)
	}
	painted := ansi.Strip(model.viewString())
	if !strings.Contains(painted, "hello from the main chat") || !strings.Contains(painted, "Group:") {
		t.Fatalf("docked frame lost a pane:\n%s", painted)
	}
	if strings.Contains(painted, "agent transcript for agent-1") {
		t.Fatalf("82-column conversation column painted a split it cannot fit:\n%s", painted)
	}
}
