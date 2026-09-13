package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/utils"
)

// TestModelAgentsHideShortcutLikeRust covers #44424: ctrl+w hides the selected
// task without stopping it, the footer advertises the shortcut, and the hide
// survives a dashboard close/reopen (but not an explicit resume).
func TestModelAgentsHideShortcutLikeRust(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
	})
	openAgentsDashboard(t, model)
	if !strings.Contains(utils.StripANSI(model.View()), "hide") {
		t.Fatalf("dashboard footer missing hide hint:\n%s", utils.StripANSI(model.View()))
	}
	if got := model.agentsOverview.SelectedThreadID(); got != "root-1" {
		t.Fatalf("initial selection = %q, want root-1", got)
	}

	model.Update(agentsKeyEvent('h'))
	if got := len(model.agentsOverview.VisibleIndices()); got != 2 {
		t.Fatalf("visible rows after hide = %d, want 2", got)
	}
	if got := model.agentsOverview.SelectedThreadID(); got != "root-2" {
		t.Fatalf("selection after hide = %q, want next visible root-2", got)
	}
	if got := len(model.agentsOverview.ThreadIDs()); got != 3 {
		t.Fatalf("rows mutated by hide = %d, want 3", got)
	}

	// Activity/metadata refreshes must not reveal the hidden root.
	model.applyAgentsOverviewList(agentsOverviewListMsg{rows: agentsOverviewTestRows(), requestID: model.agentsOverviewRefresh})
	if got := len(model.agentsOverview.VisibleIndices()); got != 2 {
		t.Fatalf("visible rows after refresh = %d, want 2", got)
	}

	// Close and reopen: the hide persists across dashboard renders.
	model.Update(key(bubbletea.KeyEsc))
	if model.agentsOverview != nil {
		t.Fatal("esc did not close the dashboard")
	}
	openAgentsDashboard(t, model)
	if got := len(model.agentsOverview.VisibleIndices()); got != 2 {
		t.Fatalf("visible rows after reopen = %d, want hidden root still hidden", got)
	}
	if got := model.agentsOverview.SelectedThreadID(); got == "root-1" {
		t.Fatalf("reopened dashboard selected the hidden root")
	}
}
