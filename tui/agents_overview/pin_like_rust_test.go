package agentsoverview

import (
	"strings"
	"testing"
)

// pinRows returns pinnable rows: the command center only offers the pin
// shortcut for thread sources that can live in a shared section.
func pinRows() []Row {
	return []Row{
		{ThreadID: "task-a", Name: "alpha", CWD: "/work/a", Group: GroupWorking, Source: "cli"},
		{ThreadID: "task-b", Name: "beta", CWD: "/work/b", Group: GroupReady, Source: "cli"},
		{ThreadID: "task-c", Name: "gamma", CWD: "/work/a", Group: GroupNeedsYou, Source: "exec"},
		{ThreadID: "task-d", Name: "delta", CWD: "/work/c", Group: GroupFinished},
	}
}

func visibleThreadIDs(view *View) []string {
	ids := make([]string, 0, len(view.Rows))
	for _, index := range view.VisibleIndices() {
		ids = append(ids, view.Rows[index].ThreadID)
	}
	return ids
}

// TestPinnedTasksRenderFirstInPinnedGroupLikeRust covers Rust #51500 ("Add
// shared task pinning to the agent command center"): pinned tasks render first
// in their own "Pinned" group, keep the shared section order, and the remaining
// tasks keep the active grouping order. Rust counterparts:
// agents_overview_grouping::visible_indices, agent_center/rows.rs group headings
// and the live_center_columns snapshot.
func TestPinnedTasksRenderFirstInPinnedGroupLikeRust(t *testing.T) {
	view := New(pinRows(), "", false)
	view.SetPinnedThreads([]string{"task-c"})
	if got := strings.Join(visibleThreadIDs(view), ","); got != "task-c,task-a,task-b,task-d" {
		t.Fatalf("pinned project grouping order = %s, want task-c,task-a,task-b,task-d", got)
	}
	if heading := view.groupHeading(GroupingProject, 2); heading != PinnedGroupHeading {
		t.Fatalf("pinned heading = %q, want %q", heading, PinnedGroupHeading)
	}
	if heading := view.groupHeading(GroupingProject, 0); heading == PinnedGroupHeading {
		t.Fatal("unpinned task must not use the Pinned heading")
	}
	// Pinned tasks form their own group regardless of the grouping preference.
	if view.sameGroup(GroupingProject, 2, 0) || view.sameGroup(GroupingStatus, 2, 0) || view.sameGroup(GroupingModel, 2, 0) {
		t.Fatal("pinned and unpinned tasks must not share a group")
	}
	if !view.sameGroup(GroupingModel, 2, 2) {
		t.Fatal("pinned task must share its group with itself")
	}

	// The rendered list shows the Pinned header first with its own count.
	rendered := strings.Join(view.Render(140, 30), "\n")
	pinnedHeader := strings.Index(rendered, "Pinned  1")
	firstProject := strings.Index(rendered, "/work/b")
	if pinnedHeader < 0 {
		t.Fatalf("Pinned header missing from render:\n%s", rendered)
	}
	if firstProject >= 0 && pinnedHeader > firstProject {
		t.Fatalf("Pinned group must render before the project groups:\n%s", rendered)
	}

	// Grouping toggles keep the pinned group first.
	view.ToggleGrouping()
	if got := strings.Join(visibleThreadIDs(view), ","); got != "task-c,task-a,task-b,task-d" {
		t.Fatalf("pinned status grouping order = %s, want pinned row first", got)
	}
	view.ToggleGrouping()
	if got := strings.Join(visibleThreadIDs(view), ","); got != "task-c,task-a,task-b,task-d" {
		t.Fatalf("pinned model grouping order = %s, want pinned row first", got)
	}
}

// TestPinnedTasksKeepSharedSectionOrderLikeRust covers the Rust rule that pinned
// tasks are ordered by their shared-section position (not by recency), while the
// unpinned rows keep the host order.
func TestPinnedTasksKeepSharedSectionOrderLikeRust(t *testing.T) {
	view := New(pinRows(), "", false)
	view.SetPinnedThreads([]string{"task-b", "task-d"})
	if got := strings.Join(visibleThreadIDs(view), ","); got != "task-b,task-d,task-a,task-c" {
		t.Fatalf("pinned order = %s, want task-b,task-d then task-a,task-c", got)
	}
	if threads := strings.Join(view.PinnedThreads(), ","); threads != "task-b,task-d" {
		t.Fatalf("PinnedThreads = %s, want task-b,task-d", threads)
	}
	// A pinned duplicate id keeps its first position.
	view.SetPinnedThreads([]string{"task-b", "task-d", "task-b"})
	if got := strings.Join(visibleThreadIDs(view), ","); got != "task-b,task-d,task-a,task-c" {
		t.Fatalf("duplicate pin order = %s, want task-b,task-d first", got)
	}
}

// TestPinnedTasksRespectSearchAndStatusFiltersLikeRust covers Rust #51500's
// requirement that search and status filters still apply to pinned tasks.
func TestPinnedTasksRespectSearchAndStatusFiltersLikeRust(t *testing.T) {
	view := New(pinRows(), "", false)
	view.SetPinnedThreads([]string{"task-c", "task-a"})
	view.State.Search = "beta"
	if got := strings.Join(visibleThreadIDs(view), ","); got != "task-b" {
		t.Fatalf("pinned search filter = %s, want task-b", got)
	}
	view.State.Search = "gamma"
	if got := strings.Join(visibleThreadIDs(view), ","); got != "task-c" {
		t.Fatalf("pinned search filter = %s, want task-c", got)
	}
	// Hidden tasks stay hidden even when pinned (Rust #44424).
	view.State.Search = ""
	hidden := view.SelectedThreadID()
	view.HideSelected()
	if seen := strings.Join(visibleThreadIDs(view), ","); strings.Contains(seen, hidden) {
		t.Fatalf("hidden pinned task %q still visible: %s", hidden, seen)
	}
}

// TestTogglePinSelectedLikeRust covers the Rust `p` shortcut semantics
// (AgentsOverviewView::handle_key_event + can_toggle_selected_pin): the toggle
// reports the new pinned state, is unavailable while a pin change is pending
// (duplicate-action suppression), and is disabled without shared pinning or for
// thread sources that cannot be shared.
func TestTogglePinSelectedLikeRust(t *testing.T) {
	view := New(pinRows(), "", false)
	// Pinning is disabled until the dashboard learns the shared pinned section.
	if view.PinsSupported() || view.CanToggleSelectedPin() {
		t.Fatal("pinning must be disabled before the shared section is known")
	}
	if _, _, ok := view.TogglePinSelected(); ok {
		t.Fatal("toggle must be unavailable while pinning is disabled")
	}

	view.SetPinnedThreads(nil)
	if !view.PinsSupported() {
		t.Fatal("an empty pinned section is still supported")
	}
	if !view.CanToggleSelectedPin() {
		t.Fatal("selected cli task should be pinnable")
	}
	threadID, pinned, ok := view.TogglePinSelected()
	if !ok || threadID != "task-a" || !pinned {
		t.Fatalf("first toggle = (%q,%v,%v), want (task-a,true,true)", threadID, pinned, ok)
	}
	// Rust #51500 duplicate-action suppression: the pending request blocks a
	// second pin action.
	if view.CanToggleSelectedPin() {
		t.Fatal("pin action must be pending after a toggle")
	}
	if _, _, ok := view.TogglePinSelected(); ok {
		t.Fatal("duplicate pin action must be suppressed while pending")
	}
	view.SetPinPending(false)
	view.ApplyPinChange(threadID, true)
	if !view.IsPinned("task-a") {
		t.Fatal("ApplyPinChange did not pin the task")
	}
	if _, pinned, ok := view.TogglePinSelected(); !ok || pinned {
		t.Fatalf("second toggle = (pinned=%v, ok=%v), want unpin request", pinned, ok)
	}
	view.SetPinPending(false)
	view.ApplyPinChange("task-a", false)
	if view.IsPinned("task-a") {
		t.Fatal("ApplyPinChange did not unpin the task")
	}

	// Tasks whose thread source cannot be shared stay unpinnable, and the
	// shortcut is disabled again when the server loses shared sections.
	view.Selected = 3
	if view.Rows[3].Source != "" {
		t.Fatalf("row 3 source = %q, want unknown", view.Rows[3].Source)
	}
	if view.CanToggleSelectedPin() {
		t.Fatal("task with an unknown source must not be pinnable")
	}
	view.Selected = 1
	view.Rows[1].Source = "subAgent"
	if view.CanToggleSelectedPin() {
		t.Fatal("subagent-sourced task must not be pinnable")
	}
	view.ClearPinnedThreads()
	if view.PinsSupported() || view.CanToggleSelectedPin() {
		t.Fatal("ClearPinnedThreads must disable pinning")
	}
}

// TestSupportsSharedPinningLikeRust covers Rust
// agents_overview_discovery::supports_shared_pinning: interactive, exec and
// app-server tasks plus the custom "atlas"/"chatgpt" sources can be pinned;
// subagents and unknown sources cannot.
func TestSupportsSharedPinningLikeRust(t *testing.T) {
	for _, source := range []string{"cli", "vscode", "exec", "appServer", "atlas", "chatgpt"} {
		if !SupportsSharedPinning(source) {
			t.Errorf("SupportsSharedPinning(%q) = false, want true", source)
		}
	}
	for _, source := range []string{"", "unknown", "subAgent", "subAgentReview", "subAgentThreadSpawn"} {
		if SupportsSharedPinning(source) {
			t.Errorf("SupportsSharedPinning(%q) = true, want false", source)
		}
	}
}

// TestRefreshKeepsPinsAndPendingStateLikeRust covers Rust #51500's refresh
// behavior: a thread-list refresh keeps the pinned-section order and any
// in-flight pin request instead of resetting them.
func TestRefreshKeepsPinsAndPendingStateLikeRust(t *testing.T) {
	view := New(pinRows(), "", false)
	view.SetPinnedThreads([]string{"task-b"})
	view.SetPinPending(true)
	view.ApplyRefresh(pinRows(), "")
	if !view.IsPinned("task-b") || !view.PinPending() {
		t.Fatalf("refresh lost pin state: pinned=%v pending=%v", view.IsPinned("task-b"), view.PinPending())
	}
	if got := strings.Join(visibleThreadIDs(view), ","); got != "task-b,task-a,task-c,task-d" {
		t.Fatalf("refresh pinned order = %s, want task-b first", got)
	}
}

// TestPinnedFooterHintLikeRust covers Rust agent_center/hints.rs: the
// "Pin/unpin" shortcut is only advertised while the selected task can be
// pinned, and it follows the configured agents.toggle_pin binding.
func TestPinnedFooterHintLikeRust(t *testing.T) {
	view := New(pinRows(), "", false)
	if rendered := strings.Join(view.Render(140, 30), "\n"); strings.Contains(rendered, "pin/unpin") {
		t.Fatalf("pin hint must not render without shared pinning:\n%s", rendered)
	}
	view.SetPinnedThreads(nil)
	if rendered := strings.Join(view.Render(140, 30), "\n"); !strings.Contains(rendered, "p pin/unpin") {
		t.Fatalf("pin hint missing:\n%s", rendered)
	}
	// A custom binding replaces the default key in the footer.
	view.SetShortcutHint(ShortcutHintTogglePin, "z p")
	if rendered := strings.Join(view.Render(140, 30), "\n"); !strings.Contains(rendered, "z p pin/unpin") {
		t.Fatalf("custom pin hint missing:\n%s", rendered)
	}
	view.SetShortcutHint(ShortcutHintTogglePin, "")
	if rendered := strings.Join(view.Render(140, 30), "\n"); strings.Contains(rendered, "pin/unpin") {
		t.Fatalf("unbound pin hint must be hidden:\n%s", rendered)
	}
	// Sources that cannot be shared are not advertised either.
	view.Selected = 3
	if rendered := strings.Join(view.Render(140, 30), "\n"); strings.Contains(rendered, "pin/unpin") {
		t.Fatalf("pin hint must be hidden for an unpinnable task:\n%s", rendered)
	}
}
