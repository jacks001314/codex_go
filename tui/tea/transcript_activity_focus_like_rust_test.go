package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func activityFocusFixture(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.AddHistoryLines(
		[]string{"Ran older", "  +2 lines (ctrl+t to view transcript)"},
		[]string{"$ older command", "older hidden detail", "older tail"},
	)
	state.AddHistoryLines(
		[]string{"Ran newer", "  +2 lines (ctrl+t to view transcript)"},
		[]string{"$ newer command", "newer hidden detail", "newer tail"},
	)
	state.Messages[0].ActivityIDs = []string{"call-older"}
	state.Messages[1].ActivityIDs = []string{"call-newer"}
	return NewModel(state, Options{Width: 80, Height: 16})
}

func TestFocusActivityKeymapLikeRust(t *testing.T) {
	descriptor, ok := codextui.FindKeymapAction("global", "focus_activity")
	if !ok {
		t.Fatal("global.focus_activity is missing from keymapActionCatalog")
	}
	if descriptor.Label != "Focus Activity" {
		t.Fatalf("focus_activity label = %q", descriptor.Label)
	}
	bindings, source, custom := codextui.ResolvedKeymapBindings(nil, "global", "focus_activity")
	if custom || source != "default" || strings.Join(bindings, ",") != "f4" {
		t.Fatalf("focus_activity default = %#v source=%q custom=%v, want f4", bindings, source, custom)
	}
}

// TestTranscriptActivityFocusDisclosureLikeRust covers Rust #46734's
// focus/navigation/expand flow for activity groups.
func TestTranscriptActivityFocusDisclosureLikeRust(t *testing.T) {
	model := activityFocusFixture(t)
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF4})
	if !model.activityFocus.active || model.focusedTranscriptActivityIndex() != 1 {
		t.Fatalf("focus = active=%v message=%d, want newest activity", model.activityFocus.active, model.focusedTranscriptActivityIndex())
	}
	if view := utils.StripANSI(model.View()); strings.Contains(view, "hidden detail") {
		t.Fatalf("focus alone expanded hidden output:\n%s", view)
	}
	footer := utils.StripANSI(model.View())
	if !strings.Contains(footer, "↑ previous · ↓ next · enter details · esc back") {
		t.Fatalf("activity footer missing:\n%s", footer)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF4})
	if model.activityFocus.active {
		t.Fatal("second F4 did not clear activity focus")
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF4})
	if !model.activityFocus.active {
		t.Fatal("F4 did not reopen activity focus")
	}

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRight})
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "newer hidden detail") {
		t.Fatalf("Right did not expand the focused activity:\n%s", view)
	}
	if strings.Contains(view, "older hidden detail") {
		t.Fatalf("Right expanded an unselected activity:\n%s", view)
	}

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyLeft})
	if view := utils.StripANSI(model.View()); strings.Contains(view, "newer hidden detail") {
		t.Fatalf("Left did not collapse the focused activity:\n%s", view)
	}

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp})
	if model.focusedTranscriptActivityIndex() != 0 {
		t.Fatalf("Up focus = %d, want 0", model.focusedTranscriptActivityIndex())
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRight})
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "older hidden detail") {
		t.Fatalf("older activity did not expand after moving focus:\n%s", view)
	}

	// Manual expansions persist while focus moves and after Esc closes the
	// controls.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyDown})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "older hidden detail") || !strings.Contains(view, "newer hidden detail") {
		t.Fatalf("manual expansions did not persist across focus movement:\n%s", view)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if model.activityFocus.active {
		t.Fatal("Esc did not clear activity focus")
	}
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "older hidden detail") || !strings.Contains(view, "newer hidden detail") {
		t.Fatalf("Esc collapsed manual disclosures:\n%s", view)
	}
}

// TestTranscriptFindClearsActivityFocusLikeRust keeps the two keyboard-owning
// transcript modes mutually exclusive.
func TestTranscriptFindClearsActivityFocusLikeRust(t *testing.T) {
	model := activityFocusFixture(t)
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF4})
	if !model.activityFocus.active {
		t.Fatal("F4 did not focus an activity")
	}
	model.Update(agentsF3Event())
	if model.activityFocus.active {
		t.Fatal("F3 did not clear activity focus")
	}
	if !model.mainFind.editing {
		t.Fatal("F3 did not start owned Find")
	}
}

// TestActivityFocusFollowsStableActivityIDsLikeRust keeps disclosure state
// keyed by tool-call identity instead of the history message position.
func TestActivityFocusFollowsStableActivityIDsLikeRust(t *testing.T) {
	model := activityFocusFixture(t)
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF4})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRight})
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "newer hidden detail") {
		t.Fatalf("newer activity was not expanded:\n%s", view)
	}

	// Replacing the rendered cell at the same index but preserving the call ID
	// keeps both focus and the explicit expansion.
	model.upsertHistoryMessageWithTranscript(
		1,
		[]string{"Ran newer", "  +2 lines (ctrl+t to view transcript)"},
		[]string{"$ newer command", "newer hidden detail updated", "newer tail"},
		[]string{"$ newer command", "newer hidden detail updated", "newer tail"},
		[]string{"call-newer"},
	)
	view := utils.StripANSI(model.View())
	if !model.activityFocus.active || model.focusedTranscriptActivityIndex() != 1 {
		t.Fatalf("focus lost across same-ID replacement: active=%v index=%d", model.activityFocus.active, model.focusedTranscriptActivityIndex())
	}
	if !strings.Contains(view, "newer hidden detail updated") {
		t.Fatalf("expansion lost across same-ID replacement:\n%s", view)
	}

	// A different cell at the same index is not allowed to inherit the old
	// focus. The old expansion remains keyed by the old ID.
	model.upsertHistoryMessageWithTranscript(
		1,
		[]string{"Ran replacement", "  +2 lines (ctrl+t to view transcript)"},
		[]string{"$ replacement", "replacement hidden detail", "replacement tail"},
		[]string{"$ replacement", "replacement hidden detail", "replacement tail"},
		[]string{"call-replacement"},
	)
	model.refreshTranscript()
	if model.activityFocus.active {
		t.Fatalf("focus followed a different activity ID at the same index")
	}
	if view := utils.StripANSI(model.View()); strings.Contains(view, "replacement hidden detail") {
		t.Fatalf("old disclosure expanded the replacement cell:\n%s", view)
	}

	// Restoring the original identity restores its manual expansion.
	model.upsertHistoryMessageWithTranscript(
		1,
		[]string{"Ran newer", "  +2 lines (ctrl+t to view transcript)"},
		[]string{"$ newer command", "newer hidden detail restored", "newer tail"},
		[]string{"$ newer command", "newer hidden detail restored", "newer tail"},
		[]string{"call-newer"},
	)
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "newer hidden detail restored") {
		t.Fatalf("restored activity ID did not recover its expansion:\n%s", view)
	}
}

// TestActivityFocusMigratesLiveGroupToHistoryLikeRust pins the live -> history
// transition: the active group is already represented by a State message with
// call IDs, and flushing it must not move focus to a later live group.
func TestActivityFocusMigratesLiveGroupToHistoryLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 16})
	model.upsertHistoryMessageWithTranscript(
		-1,
		[]string{"Ran live", "  +2 lines (ctrl+t to view transcript)"},
		[]string{"$ live", "live hidden detail", "tail"},
		[]string{"$ live", "live hidden detail", "tail"},
		[]string{"call-live"},
	)
	model.activityFollow = true
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF4})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRight})
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "live hidden detail") {
		t.Fatalf("live activity did not expand:\n%s", view)
	}

	// The active group flushes, then a different live group appears. Disclosure
	// stays with call-live rather than following the new tail.
	model.flushActiveExecCell()
	model.upsertHistoryMessageWithTranscript(
		-1,
		[]string{"Ran next", "  +2 lines (ctrl+t to view transcript)"},
		[]string{"$ next", "next hidden detail", "tail"},
		[]string{"$ next", "next hidden detail", "tail"},
		[]string{"call-next"},
	)
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "live hidden detail") {
		t.Fatalf("live disclosure was lost after history migration:\n%s", view)
	}
	if strings.Contains(view, "next hidden detail") {
		t.Fatalf("focus/disclosure migrated to the unrelated next group:\n%s", view)
	}
	if model.focusedTranscriptActivityIndex() != 0 {
		t.Fatalf("focused index = %d, want original live group at 0", model.focusedTranscriptActivityIndex())
	}
}

// Rust footer_hint::first_fitting_line drops whole navigation hints from the
// left so a narrow activity footer never shows half a shortcut.
func TestActivityFocusFooterFitsWidthLikeRust(t *testing.T) {
	model := activityFocusFixture(t)
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF4})
	const (
		all     = "\u2191 previous \u00b7 \u2193 next \u00b7 enter details \u00b7 esc back"
		next    = "\u2193 next \u00b7 enter details \u00b7 esc back"
		details = "enter details \u00b7 esc back"
		back    = "esc back"
	)
	for _, testCase := range []struct {
		width int
		want  string
	}{
		{width: 80, want: all},
		{width: 46, want: all},
		{width: 45, want: next},
		{width: 33, want: next},
		{width: 32, want: details},
		{width: 24, want: details},
		{width: 23, want: back},
		{width: 20, want: back},
		{width: 8, want: back},
		{width: 7, want: ""},
		{width: 0, want: ""},
	} {
		if got := model.activityFocusBarLine(testCase.width); got != testCase.want {
			t.Fatalf("footer at width %d = %q, want %q", testCase.width, got, testCase.want)
		}
	}
}
