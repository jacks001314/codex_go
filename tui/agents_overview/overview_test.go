package agentsoverview

import (
	"strings"
	"testing"
)

func sampleRows() []Row {
	return []Row{
		{ThreadID: "t-1", Name: "alpha", Preview: "fix the parser", CWD: "/work/a", Group: GroupWorking, StatusActive: true},
		{ThreadID: "t-2", Name: "beta", Preview: "review pr", CWD: "/work/a", Group: GroupReady},
		{ThreadID: "t-3", Name: "gamma", Preview: "needs approval", CWD: "/work/b", Group: GroupNeedsYou, StatusActive: true},
		{ThreadID: "t-4", Name: "", Preview: "", CWD: "/work/c", Group: GroupFinished},
	}
}

func TestGroupForStatusLikeRust(t *testing.T) {
	cases := []struct {
		status           string
		waitingApproval  bool
		waitingUserInput bool
		want             Group
		wantLabel        string
	}{
		{"active", true, false, GroupNeedsYou, "Needs input"},
		{"active", false, true, GroupNeedsYou, "Needs input"},
		{"active", false, false, GroupWorking, "Working"},
		{"idle", false, false, GroupReady, "Ready"},
		{"systemError", false, false, GroupNeedsYou, "Needs input"},
		{"notLoaded", false, false, GroupFinished, "Finished"},
		{"", false, false, GroupFinished, "Finished"},
	}
	for _, tc := range cases {
		if got := GroupForStatus(tc.status, tc.waitingApproval, tc.waitingUserInput); got != tc.want {
			t.Errorf("GroupForStatus(%q) = %v, want %v", tc.status, got, tc.want)
		}
		if GroupForStatus(tc.status, tc.waitingApproval, tc.waitingUserInput).Label() != tc.wantLabel {
			t.Errorf("GroupForStatus(%q).Label() = %q, want %q", tc.status, GroupForStatus(tc.status, tc.waitingApproval, tc.waitingUserInput).Label(), tc.wantLabel)
		}
	}
}

func TestCountsAndTitle(t *testing.T) {
	view := New(sampleRows(), "", false)
	needsYou, working, ready := view.Counts()
	if needsYou != 1 || working != 1 || ready != 1 {
		t.Fatalf("Counts = %d/%d/%d, want 1/1/1", needsYou, working, ready)
	}
	if got := sampleRows()[3].Title(); got != "Untitled task" {
		t.Fatalf("empty row Title = %q, want Untitled task", got)
	}
	if got := sampleRows()[0].Title(); got != "alpha" {
		t.Fatalf("Title = %q, want alpha", got)
	}
}

func TestSearchFiltersRows(t *testing.T) {
	view := New(sampleRows(), "", false)
	if got := len(view.VisibleIndices()); got != 4 {
		t.Fatalf("VisibleIndices before search = %d, want 4", got)
	}
	view.ToggleSearch()
	if !view.State.Searching {
		t.Fatal("ToggleSearch did not enter searching mode")
	}
	for _, ch := range "review" {
		view.TypeChar(ch)
	}
	visible := view.VisibleIndices()
	if len(visible) != 1 || strings.TrimSpace(view.Rows[visible[0]].ThreadID) != "t-2" {
		t.Fatalf("search 'review' visible = %v, want [t-2]", visible)
	}
	// Esc clears search
	view.Cancel()
	if view.State.Searching || view.State.Search != "" {
		t.Fatalf("Cancel did not exit search: %#v", view.State)
	}
	if got := len(view.VisibleIndices()); got != 4 {
		t.Fatalf("VisibleIndices after search cancel = %d, want 4", got)
	}
}

func TestProjectGroupingSortsByCWD(t *testing.T) {
	rows := []Row{
		{ThreadID: "t-b", CWD: "/z", Group: GroupReady},
		{ThreadID: "t-a", CWD: "/a", Group: GroupWorking},
		{ThreadID: "t-c", CWD: "/m", Group: GroupReady},
	}
	view := New(rows, "", false)
	visible := view.VisibleIndices()
	got := []string{}
	for _, index := range visible {
		got = append(got, view.Rows[index].CWD)
	}
	want := []string{"/a", "/m", "/z"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("project ordering = %v, want %v", got, want)
	}
	// status grouping keeps host order
	view.ToggleGrouping()
	if !view.State.StatusGrouping {
		t.Fatal("ToggleGrouping did not enable status grouping")
	}
	visible = view.VisibleIndices()
	if len(visible) != 3 {
		t.Fatalf("status-grouping visible = %d, want 3", len(visible))
	}
}

func TestActivateDispatchLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	for _, ch := range "add tests" {
		view.TypeChar(ch)
	}
	if action := view.Activate(); action != ActionDispatchTask {
		t.Fatalf("Activate with input = %v, want ActionDispatchTask", action)
	}
	if view.State.Input != "" {
		t.Fatalf("input not cleared after dispatch: %q", view.State.Input)
	}
	// All-whitespace input is ignored.
	view.TypeChar(' ')
	if action := view.Activate(); action != ActionNone {
		t.Fatalf("Activate with whitespace input = %v, want none", action)
	}
}

func TestActivateOpenSelectedLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.Selected = 2
	if action := view.Activate(); action != ActionOpenThread {
		t.Fatalf("Activate with empty input = %v, want ActionOpenThread", action)
	}
	if view.Completion != CompletionAccepted {
		t.Fatalf("Completion = %v, want accepted", view.Completion)
	}
}

func TestRenameFlowLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.Selected = 1
	if !view.BeginRename() {
		t.Fatal("BeginRename returned false")
	}
	if view.State.Renaming != true || view.State.Input != "beta" {
		t.Fatalf("rename state = %#v", view.State)
	}
	view.State.Input = "beta v2"
	if action := view.Activate(); action != ActionRenameThread {
		t.Fatalf("Activate while renaming = %v, want ActionRenameThread", action)
	}
	if view.State.Renaming || view.State.Input != "" {
		t.Fatalf("rename not cleared: %#v", view.State)
	}
	// Renaming blocks navigation (Rust move_selection gate).
	view.BeginRename()
	before := view.Selected
	view.MoveSelection(true)
	if view.Selected != before {
		t.Fatal("MoveSelection moved while renaming")
	}
	view.Cancel() // cancel rename
	if view.State.Renaming || view.State.Input != "" {
		t.Fatalf("Cancel did not clear rename: %#v", view.State)
	}
}

func TestStopOnlyWhenActiveLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.Selected = 0 // t-1 working/active
	if action := view.StopSelected(); action != ActionStopThread {
		t.Fatalf("StopSelected active = %v, want ActionStopThread", action)
	}
	view.Selected = 1 // t-2 ready
	if action := view.StopSelected(); action != ActionNone {
		t.Fatalf("StopSelected ready = %v, want none", action)
	}
}

func TestCancelExitSemantics(t *testing.T) {
	standalone := New(sampleRows(), "", true)
	embedded := New(sampleRows(), "", false)
	if action := standalone.Cancel(); action != ActionExit {
		t.Fatalf("standalone Cancel = %v, want ActionExit", action)
	}
	if standalone.Completion != CompletionCancelled {
		t.Fatalf("standalone completion = %v, want cancelled", standalone.Completion)
	}
	if action := embedded.Cancel(); action != ActionNone {
		t.Fatalf("embedded Cancel = %v, want none", action)
	}
	// Cancel with input clears first.
	view := New(sampleRows(), "", true)
	view.TypeChar('x')
	if action := view.Cancel(); action != ActionNone || view.State.Input != "" {
		t.Fatalf("Cancel with input = %v input=%q", action, view.State.Input)
	}
}

func TestClearNewResetsEverything(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.ToggleSearch()
	view.State.Search = "abc"
	view.State.Input = "hello"
	view.State.Renaming = true
	view.ClearNew()
	if view.State.Search != "" || view.State.Searching || view.State.Renaming || view.State.Input != "" {
		t.Fatalf("ClearNew left state: %#v", view.State)
	}
}

func TestApplyRefreshPreservesSelection(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.Selected = 2 // t-3
	refreshed := []Row{
		{ThreadID: "t-1", Name: "alpha", CWD: "/work/a", Group: GroupWorking, StatusActive: true},
		{ThreadID: "t-2", Name: "beta", CWD: "/work/a", Group: GroupReady},
		{ThreadID: "t-3", Name: "gamma v2", CWD: "/work/b", Group: GroupReady},
		{ThreadID: "t-5", Name: "new task", CWD: "/work/d", Group: GroupWorking, StatusActive: true},
	}
	view.ApplyRefresh(refreshed, "t-3")
	if got := view.SelectedThreadID(); got != "t-3" {
		t.Fatalf("selection after refresh = %q, want t-3", got)
	}
	// Renaming cancels when the selected thread disappears.
	view.State.Renaming = true
	view.State.Input = "x"
	view.ApplyRefresh([]Row{{ThreadID: "t-9", Name: "other", CWD: "/x", Group: GroupReady}}, "")
	if view.State.Renaming || view.State.Input != "" {
		t.Fatalf("rename not cancelled on vanished selection: %#v", view.State)
	}
}

func TestRenderLayout(t *testing.T) {
	view := New(sampleRows(), "", true)
	lines := view.Render(120, 24)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"Agent command center",
		"1 need input   1 working   1 ready",
		"New task › ",
		"Describe a task and press enter to dispatch it",
		"↑↓ navigate  enter open  ctrl+f search  ctrl+s group  ctrl+r rename  ctrl+x stop  esc back",
		"/work/a  2",
		"› ● alpha  Working",
		"/work/b  1",
		"/work/c  1",
		"✓ Untitled task",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("render missing %q:\n%s", want, joined)
		}
	}
	// Details pane appears on wide terminals.
	for _, want := range []string{"Task details", "Latest activity", "fix the parser"} {
		if !strings.Contains(joined, want) {
			t.Errorf("details pane missing %q:\n%s", want, joined)
		}
	}
}

func TestRenderDetailsShowsNoActivityPlaceholder(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.Selected = 3 // t-4 has no name/preview
	lines := view.Render(120, 24)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Task details", "Untitled task", "No activity yet."} {
		if !strings.Contains(joined, want) {
			t.Errorf("details pane missing %q:\n%s", want, joined)
		}
	}
}

func TestRenderStatusGrouping(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.ToggleGrouping()
	lines := view.Render(120, 24)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Needs input  1", "Working  1", "Ready  1", "Finished  1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("status grouping missing %q:\n%s", want, joined)
		}
	}
}

func TestRenderNarrowTerminalSkipsDetails(t *testing.T) {
	view := New(sampleRows(), "", false)
	lines := view.Render(80, 20)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "Task details") {
		t.Errorf("details pane rendered on narrow terminal:\n%s", joined)
	}
	if strings.Contains(joined, "Latest activity") {
		t.Errorf("activity rendered on narrow terminal:\n%s", joined)
	}
}

func TestRenderTooSmallReturnsNothing(t *testing.T) {
	view := New(sampleRows(), "", false)
	if got := view.Render(10, 5); got != nil {
		t.Fatalf("Render(10,5) = %v, want nil", got)
	}
}

func TestPasteSanitizesNewlines(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.Paste("line1\nline2\r\nline3")
	if view.State.Input != "line1 line2 line3" {
		t.Fatalf("Paste = %q, want sanitized", view.State.Input)
	}
}
