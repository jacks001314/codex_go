package tea

import (
	"fmt"
	"strings"
	"testing"

	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

func discoveryRows(count int) []agentsoverview.Row {
	rows := make([]agentsoverview.Row, 0, count)
	for i := 0; i < count; i++ {
		rows = append(rows, agentsoverview.Row{
			ThreadID:   fmt.Sprintf("task-%02d", i),
			Name:       fmt.Sprintf("task %02d", i),
			CWD:        "/work/a",
			Group:      agentsoverview.GroupReady,
			StatusType: "idle",
			UpdatedAt:  int64(1000 - i),
		})
	}
	return rows
}

// Rust #49106: the command center publishes ten tasks plus a "Show more" row,
// and activating the row publishes the next page.
func TestAgentsOverviewShowMoreLikeRust(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 30,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return discoveryRows(25), nil
		},
	})
	openAgentsDashboard(t, model)
	if visible := len(model.agentsOverview.VisibleIndices()); visible != 10 {
		t.Fatalf("visible tasks = %d, want 10", visible)
	}
	if !model.agentsOverview.HasMore() {
		t.Fatal("the dashboard did not report more history")
	}
	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, "Show more") {
		t.Fatalf("Show more row missing:\n%s", view)
	}
	updated, _ := model.Update(key(keymsg.KeyEnd))
	model = updated.(*Model)
	if !model.agentsOverview.ShowMoreSelected() {
		t.Fatalf("End did not select the Show more row (selected=%d)", model.agentsOverview.Selected)
	}
	updated, command := model.Update(key(keymsg.KeyEnter))
	model = updated.(*Model)
	if command != nil {
		updated, _ = model.Update(command())
		model = updated.(*Model)
	}
	if visible := len(model.agentsOverview.VisibleIndices()); visible != 20 {
		t.Fatalf("visible tasks after Show more = %d, want 20", visible)
	}
	if !model.agentsOverview.HasMore() {
		t.Fatal("a partial second page should still offer more history")
	}
}
