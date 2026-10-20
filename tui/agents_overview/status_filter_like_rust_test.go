package agentsoverview

import (
	"strings"
	"testing"
	"time"
)

func statusFilterRows() []Row {
	return []Row{
		{ThreadID: "needs", Name: "needs", CWD: "/work/a", Group: GroupNeedsYou},
		{ThreadID: "working", Name: "working", CWD: "/work/a", Group: GroupWorking, StatusActive: true},
		{ThreadID: "ready", Name: "ready", CWD: "/work/b", Group: GroupReady},
		{ThreadID: "inactive", Name: "inactive", CWD: "/work/c", Group: GroupFinished},
	}
}

func visibleFilterThreads(view *View) string {
	var ids []string
	for _, index := range view.VisibleIndices() {
		ids = append(ids, view.Rows[index].ThreadID)
	}
	return strings.Join(ids, ",")
}

// TestStatusFilterCombinesWithSearchAndHideLikeRust covers #46839: status,
// search, hide, and pinning all apply together.
func TestStatusFilterCombinesWithSearchAndHideLikeRust(t *testing.T) {
	view := New(statusFilterRows(), "", false)
	view.State.StatusFilter = StatusFilterWorking
	if got := visibleFilterThreads(view); got != "working" {
		t.Fatalf("working filter = %q", got)
	}

	view.State.Search = "ready"
	if got := visibleFilterThreads(view); got != "" {
		t.Fatalf("search + working filter = %q, want none", got)
	}
	view.State.Search = "work"
	if got := visibleFilterThreads(view); got != "working" {
		t.Fatalf("search + working filter = %q", got)
	}

	// Pinning does not escape the active status tab.
	view.State.Search = ""
	view.pinRanks = map[string]int{"needs": 0}
	if got := visibleFilterThreads(view); got != "working" {
		t.Fatalf("pinned needs-you row escaped working filter: %q", got)
	}

	view.State.HiddenThreads = map[string]struct{}{"working": {}}
	if got := visibleFilterThreads(view); got != "" {
		t.Fatalf("hidden working row = %q, want none", got)
	}
}

// TestStatusFilterCyclesAndRendersCountedTabsLikeRust covers the All/Needs
// you/Working/Ready/Inactive cycle and counted tab labels.
func TestStatusFilterCyclesAndRendersCountedTabsLikeRust(t *testing.T) {
	view := New(statusFilterRows(), "working", false)
	if got := view.StatusFilterLabels(); strings.Join(got, "|") != "All 4|Needs you 1|Working 1|Ready 1|Inactive 1" {
		t.Fatalf("labels = %#v", got)
	}
	view.CycleStatusFilter(true)
	if view.State.StatusFilter != StatusFilterNeedsYou {
		t.Fatalf("forward filter = %d", view.State.StatusFilter)
	}
	if got := visibleFilterThreads(view); got != "needs" {
		t.Fatalf("needs-you filter = %q", got)
	}
	view.CycleStatusFilter(false)
	if view.State.StatusFilter != StatusFilterAll {
		t.Fatalf("backward filter = %d", view.State.StatusFilter)
	}
	view.CycleStatusFilter(false)
	if view.State.StatusFilter != StatusFilterInactive {
		t.Fatalf("wrapped filter = %d, want inactive", view.State.StatusFilter)
	}

	view.FilterHint = "tab/shift+tab filter"
	rendered := strings.Join(view.Render(120, 24), "\n")
	for _, want := range []string{"All 4", "Needs you 1", "Working 1", "Ready 1", "Inactive 1", "tab/shift+tab filter"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered tabs missing %q:\n%s", want, rendered)
		}
	}
}

// TestStatusFilterKeepsRenameTargetVisibleLikeRust covers the rule that a task
// leaving the active status group stays visible until its rename ends.
func TestStatusFilterKeepsRenameTargetVisibleLikeRust(t *testing.T) {
	rows := statusFilterRows()
	view := New(rows, "working", false)
	view.State.StatusFilter = StatusFilterWorking
	if !view.BeginRename() {
		t.Fatal("rename did not start")
	}
	view.Rows[view.Selected].Group = GroupFinished
	if got := visibleFilterThreads(view); got != "working" {
		t.Fatalf("rename target left the filter: %q", got)
	}
	view.State.Input = "renamed"
	if action := view.Activate(); action != ActionRenameThread {
		t.Fatalf("activate = %v", action)
	}
	if view.State.RenameTarget != "" {
		t.Fatalf("rename target = %q, want cleared", view.State.RenameTarget)
	}
	if got := visibleFilterThreads(view); got != "" {
		t.Fatalf("finished renamed row stayed visible: %q", got)
	}
}

// TestPageNavigationUsesViewportAndStopsAtEndsLikeRust covers #46837: paging
// moves by the rendered list height and never wraps.
func TestPageNavigationUsesViewportAndStopsAtEndsLikeRust(t *testing.T) {
	rows := make([]Row, 0, 8)
	for index := 0; index < 8; index++ {
		rows = append(rows, Row{
			ThreadID: "row-" + formatCount(index),
			Name:     "row-" + formatCount(index),
			CWD:      "/work/a",
			Group:    GroupWorking,
		})
	}
	view := New(rows, "row-0", false)
	view.pageHeight = 3
	view.PageDown()
	if got := view.SelectedThreadID(); got != "row-3" {
		t.Fatalf("PageDown selected %q, want row-3", got)
	}
	view.PageDown()
	view.PageDown()
	if got := view.SelectedThreadID(); got != "row-7" {
		t.Fatalf("PageDown did not stop at the end: %q", got)
	}
	view.PageUp()
	if got := view.SelectedThreadID(); got != "row-4" {
		t.Fatalf("PageUp selected %q, want row-4", got)
	}
	view.PageUp()
	view.PageUp()
	if got := view.SelectedThreadID(); got != "row-0" {
		t.Fatalf("PageUp did not stop at the start: %q", got)
	}
}

// TestSystemErrorRendersErrorLikeRust covers the #46837 status wording.
func TestSystemErrorRendersErrorLikeRust(t *testing.T) {
	view := New([]Row{{
		ThreadID:   "error",
		Name:       "broken",
		CWD:        "/work/a",
		Group:      GroupNeedsYou,
		StatusType: "systemError",
	}}, "", false)
	rendered := strings.Join(view.Render(120, 24), "\n")
	if !strings.Contains(rendered, "Error") || strings.Contains(rendered, "Needs input") {
		t.Fatalf("system error status wording:\n%s", rendered)
	}
}

func TestCommandCenterColumnsIncludeRelativeUpdateLikeRust(t *testing.T) {
	now := time.Now().Unix()
	view := New([]Row{{
		ThreadID:  "alpha",
		Name:      "alpha",
		CWD:       "/work/a",
		Group:     GroupWorking,
		UpdatedAt: now - 125,
	}}, "", false)
	rendered := strings.Join(view.Render(120, 24), "\n")
	if !strings.Contains(rendered, "Working") || !strings.Contains(rendered, "2m ago") {
		t.Fatalf("row columns missing status/updated:\n%s", rendered)
	}
	if got := relativeUpdateTime(0); got != "-" {
		t.Fatalf("missing timestamp = %q", got)
	}
	if got := relativeUpdateTime(now + 10); got != "now" {
		t.Fatalf("future timestamp = %q", got)
	}
}

func TestCommandCenterHelpReferenceLikeRust(t *testing.T) {
	view := New(statusFilterRows(), "", false)
	view.SetWorktreesEnabled(true)
	view.ToggleHelp()
	rendered := strings.Join(view.Render(120, 30), "\n")
	for _, want := range []string{"Task shortcuts", "Navigate", "Tasks", "View", "New worktree", "Delete"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("help missing %q:\n%s", want, rendered)
		}
	}
	view.ToggleHelp()
	if strings.Contains(strings.Join(view.Render(120, 30), "\n"), "Task shortcuts") {
		t.Fatal("help did not close")
	}
}
