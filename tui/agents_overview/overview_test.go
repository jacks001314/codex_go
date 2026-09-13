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

// TestPreviewMarkdownLikeRust covers #44752's bounded preview: control
// characters other than newlines and tabs are stripped, the text is capped at
// 512 characters, and the details prompt keeps explicit line breaks while being
// limited to two rendered lines.
func TestPreviewMarkdownLikeRust(t *testing.T) {
	if got := PreviewMarkdown("a\x00b\tc\nd\x07e"); got != "ab\tc\nde" {
		t.Fatalf("PreviewMarkdown = %q, want ab\\tc\\nde", got)
	}
	if got := PreviewMarkdown(strings.Repeat("x", 600)); len([]rune(got)) != 512 {
		t.Fatalf("PreviewMarkdown length = %d, want 512", len([]rune(got)))
	}
	// Filtered control characters do not consume the preview budget.
	mixed := "\x00" + strings.Repeat("y", 512)
	if got := PreviewMarkdown(mixed); got != strings.Repeat("y", 512) {
		t.Fatalf("PreviewMarkdown(mixed) length = %d, want 512", len([]rune(got)))
	}

	view := New([]Row{{ThreadID: "t-1", CWD: "/work/a", Preview: "alpha beta\ngamma delta\nepsilon"}}, "", false)
	lines := view.Render(140, 30)
	promptIndex := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "Prompt" {
			promptIndex = i
			break
		}
	}
	if promptIndex < 0 {
		t.Fatalf("details prompt section missing:\n%s", strings.Join(lines, "\n"))
	}
	var section []string
	for _, line := range lines[promptIndex+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		section = append(section, trimmed)
	}
	if len(section) != 2 || section[0] != "alpha beta" || section[1] != "\u2026" {
		t.Fatalf("prompt preview section = %#v, want [alpha beta, …]", section)
	}
}

// TestModelGroupingLikeRust covers #44957: Ctrl+S cycles project -> status ->
// model, model grouping sorts by model with "Unknown" for missing names, the
// footer and details report the model, and the cycle wraps back to project.
func TestModelGroupingLikeRust(t *testing.T) {
	rows := []Row{
		{ThreadID: "t-1", Name: "alpha", CWD: "/work/a", Model: "gpt-5.4", Group: GroupWorking},
		{ThreadID: "t-2", Name: "beta", CWD: "/work/b", Model: "gpt-5.5", Group: GroupReady},
		{ThreadID: "t-3", Name: "gamma", CWD: "/work/c", Model: "gpt-5.4", Group: GroupReady},
		{ThreadID: "t-4", Name: "delta", CWD: "/work/d", Group: GroupFinished},
	}
	view := New(rows, "", false)
	if view.State.Grouping != GroupingProject {
		t.Fatalf("default grouping = %v, want project", view.State.Grouping)
	}
	view.ToggleGrouping()
	if view.State.Grouping != GroupingStatus {
		t.Fatalf("first toggle = %v, want status", view.State.Grouping)
	}
	view.ToggleGrouping()
	if view.State.Grouping != GroupingModel {
		t.Fatalf("second toggle = %v, want model", view.State.Grouping)
	}

	// Model grouping orders by model name, keeping host order within a group.
	visible := view.VisibleIndices()
	order := make([]string, 0, len(visible))
	for _, index := range visible {
		order = append(order, view.Rows[index].ThreadID)
	}
	// "Unknown" sorts before lowercase model names, matching Rust's byte-wise
	// group key.
	want := []string{"t-4", "t-1", "t-3", "t-2"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("model grouping order = %v, want %v", order, want)
	}

	joined := strings.Join(view.Render(140, 30), "\n")
	for _, wantText := range []string{"gpt-5.4  2", "gpt-5.5  1", "Unknown  1", "g group: model"} {
		if !strings.Contains(joined, wantText) {
			t.Errorf("model grouping render missing %q:\n%s", wantText, joined)
		}
	}

	// The cycle wraps back to project grouping.
	view.ToggleGrouping()
	if view.State.Grouping != GroupingProject {
		t.Fatalf("third toggle = %v, want project", view.State.Grouping)
	}
	if !strings.Contains(strings.Join(view.Render(140, 30), "\n"), "g group: project") {
		t.Error("project grouping footer hint missing")
	}

	// Task details report the model, using Unknown when it is missing.
	details := strings.Join(view.Render(140, 30), "\n")
	if !strings.Contains(details, "Model: gpt-5.4") {
		t.Errorf("model detail missing:\n%s", details)
	}
	empty := New([]Row{{ThreadID: "t-9", Name: "n", CWD: "/work/z"}}, "", false)
	if !strings.Contains(strings.Join(empty.Render(140, 30), "\n"), "Model: Unknown") {
		t.Error("missing model should render as Unknown")
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
	if view.State.Grouping != GroupingStatus {
		t.Fatalf("ToggleGrouping = %v, want status", view.State.Grouping)
	}
	visible = view.VisibleIndices()
	if len(visible) != 3 {
		t.Fatalf("status-grouping visible = %d, want 3", len(visible))
	}
}

// Rust #45255: the command center no longer composes tasks, so typing does not
// fill an input and Enter opens the selected session instead of dispatching.
func TestActivateOpensWithoutComposerLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	for _, ch := range "add tests" {
		view.TypeChar(ch)
	}
	if view.State.Input != "" {
		t.Fatalf("browsing accepted text input: %q", view.State.Input)
	}
	if action := view.Activate(); action != ActionOpenThread {
		t.Fatalf("Activate = %v, want ActionOpenThread", action)
	}
	// An all-whitespace rename cannot be applied and leaves the editor open.
	view.BeginRename()
	view.State.Input = "   "
	if action := view.Activate(); action != ActionNone {
		t.Fatalf("Activate with whitespace rename = %v, want none", action)
	}
	if !view.State.Renaming {
		t.Fatal("a whitespace-only rename must leave the editor open")
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
	// Cancel with an active rename clears the editor first.
	view := New(sampleRows(), "", true)
	view.BeginRename()
	if action := view.Cancel(); action != ActionNone || view.State.Input != "" || view.State.Renaming {
		t.Fatalf("Cancel while renaming = %v state=%#v", action, view.State)
	}
}

// Rust #45255 removed the new-task shortcut's state reset: only search and
// rename own the editor, and browsing ignores text entirely.
func TestTextInputOnlyEditsMetadataLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.TypeChar('x')
	view.Backspace()
	view.Paste("pasted text")
	if view.State.Input != "" || view.State.Search != "" {
		t.Fatalf("browsing accepted text input: %#v", view.State)
	}

	view.ToggleSearch()
	view.TypeChar('x')
	if view.State.Search != "x" {
		t.Fatalf("search input = %q, want x", view.State.Search)
	}
	view.Cancel()

	view.BeginRename()
	view.State.Input = ""
	view.TypeChar('y')
	if view.State.Input != "y" || !view.State.Renaming {
		t.Fatalf("rename input = %q renaming=%v, want y", view.State.Input, view.State.Renaming)
	}
	view.Backspace()
	if view.State.Input != "" {
		t.Fatalf("rename backspace = %q, want empty", view.State.Input)
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

// Mirrors Rust #44344/#45255: Right opens the selected task whenever the list
// owns the keys; metadata editing keeps Right for the editor.
func TestCanOpenWithRightRequiresListFocusLikeRust(t *testing.T) {
	view := New(sampleRows(), "", true)
	if !view.CanOpenWithRight() {
		t.Fatal("a selected row must allow Right to open")
	}
	view.ToggleSearch()
	if view.CanOpenWithRight() {
		t.Fatal("search must keep Right for the editor")
	}
	view.Cancel()
	view.BeginRename()
	if view.CanOpenWithRight() {
		t.Fatal("metadata editing must keep Right for the editor")
	}
	if New(nil, "", true).CanOpenWithRight() {
		t.Fatal("a dashboard without a selected row must not allow Right to open")
	}
}

func TestRenderLayout(t *testing.T) {
	view := New(sampleRows(), "", true)
	lines := view.Render(120, 24)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"Agent command center",
		"1 need input   1 working   1 ready",
		"n new",
		"h hide",
		"a archive",
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
	for _, want := range []string{"Task details", "Prompt", "fix the parser"} {
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
	for _, want := range []string{"Task details", "Untitled task", "No prompt available."} {
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
	if strings.Contains(joined, "Prompt") {
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
	view.ToggleSearch()
	view.Paste("line1\nline2\r\nline3")
	if view.State.Search != "line1 line2 line3" {
		t.Fatalf("Paste = %q, want sanitized", view.State.Search)
	}
	// Rust #45255: without the task composer a paste outside the editor is a
	// no-op.
	browsing := New(sampleRows(), "", false)
	browsing.Paste("ignored")
	if browsing.State.Input != "" || browsing.State.Search != "" {
		t.Fatalf("browsing accepted a paste: %#v", browsing.State)
	}
}

// Rust #45276: creating a worktree replaces the counts summary with progress,
// pauses the footer actions, and only advertises the new-worktree shortcut when
// worktree support is enabled.
func TestCreatingWorktreeStateLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.SetWorktreesEnabled(true)
	view.SetCreatingWorktree(true)
	rendered := strings.Join(view.Render(120, 24), "\n")
	if !strings.Contains(rendered, "Creating worktree\u2026") {
		t.Fatalf("render missing the worktree progress:\n%s", rendered)
	}
	if strings.Contains(rendered, "need input") {
		t.Fatalf("progress did not replace the counts summary:\n%s", rendered)
	}
	if !strings.Contains(rendered, "ctrl-c quit") || strings.Contains(rendered, "w new worktree") {
		t.Fatalf("worktree progress footer =\n%s", rendered)
	}

	view.SetCreatingWorktree(false)
	rendered = strings.Join(view.Render(120, 24), "\n")
	if !strings.Contains(rendered, "w new worktree") {
		t.Fatalf("worktree shortcut hint missing with worktrees enabled:\n%s", rendered)
	}
	if strings.Contains(rendered, "Creating worktree") {
		t.Fatalf("progress state leaked into the idle render:\n%s", rendered)
	}

	grouped := New(sampleRows(), "", false)
	if strings.Contains(strings.Join(grouped.Render(120, 24), "\n"), "w new worktree") {
		t.Fatal("the worktree hint must not appear without worktree support")
	}
}
