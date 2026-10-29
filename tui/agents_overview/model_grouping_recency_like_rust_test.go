package agentsoverview

import (
	"strings"
	"testing"
)

// Mirrors Rust #44957 `overview_model_grouping_shows_details_and_preserves_selection`:
// within one model the newest task comes first, "Unknown" groups a missing
// model, and navigation walks the grouped order across model groups.
func TestModelGroupingOrdersNewestFirstWithinGroupLikeRust(t *testing.T) {
	// Host order deliberately disagrees with recency so the sort is exercised.
	rows := []Row{
		{ThreadID: "t-1", Name: "Older task", CWD: "/work/a", Model: "model-a", UpdatedAt: 1, Group: GroupWorking},
		{ThreadID: "t-2", Name: "Other model", CWD: "/work/a", Model: "model-b", UpdatedAt: 2, Group: GroupWorking},
		{ThreadID: "t-3", Name: "Recent task", CWD: "/work/a", Model: "model-a", UpdatedAt: 3, Group: GroupWorking},
		{ThreadID: "t-4", Name: "Legacy task", CWD: "/work/a", UpdatedAt: 4, Group: GroupWorking},
	}
	view := New(rows, "t-1", false)
	view.ToggleGrouping() // project -> directory
	view.ToggleGrouping() // directory -> status
	view.ToggleGrouping() // status -> model
	if view.State.Grouping != GroupingModel {
		t.Fatalf("grouping = %v, want model", view.State.Grouping)
	}

	order := make([]string, 0, len(rows))
	for _, index := range view.VisibleIndices() {
		order = append(order, view.Rows[index].ThreadID)
	}
	// "Unknown" sorts before lowercase model names; model-a keeps its newer task
	// first even though the host listed the older one first.
	want := []string{"t-4", "t-3", "t-1", "t-2"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("model grouping order = %v, want %v", order, want)
	}

	// Same-group membership follows the model; indices address view.Rows.
	if !view.sameGroup(GroupingModel, 0, 2) {
		t.Fatal("tasks sharing a model must share a model group")
	}
	if view.sameGroup(GroupingModel, 0, 1) || view.sameGroup(GroupingModel, 0, 3) {
		t.Fatal("different models must not share a group")
	}
	// The selected task stays selected while the grouping changes the order.
	if got := view.SelectedThreadID(); got != "t-1" {
		t.Fatalf("selection = %q, want t-1", got)
	}

	// Navigation follows the grouped order across group boundaries.
	for _, step := range []struct {
		forward bool
		want    string
	}{
		{forward: false, want: "t-3"},
		{forward: true, want: "t-1"},
		{forward: true, want: "t-2"},
		{forward: true, want: "t-4"},
	} {
		view.MoveSelection(step.forward)
		if got := view.SelectedThreadID(); got != step.want {
			t.Fatalf("MoveSelection(%v) selected %q, want %q", step.forward, got, step.want)
		}
	}

	// Model grouping also orders by recency for a project/directory grouping,
	// which shares the same key-then-recency rule.
	projectRows := []Row{
		{ThreadID: "p-1", CWD: "/work/one", UpdatedAt: 10, Group: GroupWorking},
		{ThreadID: "p-2", CWD: "/work/two", UpdatedAt: 20, Group: GroupWorking},
		{ThreadID: "p-3", CWD: "/work/one", UpdatedAt: 30, Group: GroupWorking},
	}
	project := New(projectRows, "", false)
	projectOrder := make([]string, 0, len(projectRows))
	for _, index := range project.VisibleIndices() {
		projectOrder = append(projectOrder, project.Rows[index].ThreadID)
	}
	if strings.Join(projectOrder, ",") != "p-3,p-1,p-2" {
		t.Fatalf("project grouping order = %v, want [p-3 p-1 p-2]", projectOrder)
	}
}
