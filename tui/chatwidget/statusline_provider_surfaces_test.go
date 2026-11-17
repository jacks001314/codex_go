package chatwidget

import (
	"strings"
	"testing"
	"time"
)

// Rust #61979: a selected statusline provider consumes the workspace headline
// even when no built-in status-line item asks for it.
func TestStatusControlsProviderRequestsWorkspaceHeadlineWithoutItemsLikeRust(t *testing.T) {
	state := NewStatusControlsState(StatusControlsRuntime{
		CWD:                 "/repo",
		ModelName:           "gpt-5",
		HasCodexBackendAuth: true,
	})
	state.StatusLineConfigured = true
	state.StatusLineIDs = []string{}
	state.StatusLineProviderActive = true
	selections := NewStatusSurfaceSelections(state.ConfiguredStatusLineItems(), state.ConfiguredTerminalTitleItems())
	if selections.UsesWorkspaceHeadline() {
		t.Fatal("test setup still configures the built-in workspace headline item")
	}
	requested, requestID := state.syncWorkspaceHeadlineState(selections, time.Unix(1000, 0))
	if !requested || requestID != 0 {
		t.Fatalf("provider did not request the workspace headline: requested=%v id=%d", requested, requestID)
	}

	update := state.SetStatusLineWorkspaceHeadline(requestID, WorkspaceHeadlineFetchResult{
		Kind:     WorkspaceHeadlineFetchAvailable,
		Headline: "Ship parity",
	})
	if !update.Matched || !update.ScheduleWorkspaceHeadlineRefresh {
		t.Fatalf("provider headline update = %#v", update)
	}
	if state.Runtime.WorkspaceHeadline != "Ship parity" {
		t.Fatalf("headline = %q", state.Runtime.WorkspaceHeadline)
	}
}

// Rust #61979: refresh_status_line_from_selections returns early when a
// provider owns the footer, so the native span line is not rebuilt.
func TestStatusControlsProviderOwnsStatusLineLikeRust(t *testing.T) {
	state := NewStatusControlsState(StatusControlsRuntime{
		CWD:       "/repo",
		ModelName: "gpt-5",
	})
	state.StatusLineConfigured = true
	state.StatusLineIDs = []string{"model", "current-dir"}
	state.StatusLineUseThemeColors = true
	state.StatusLineProviderActive = true

	result := state.RefreshStatusSurfaces()
	if !result.StatusLineEnabled {
		t.Fatal("provider footer must keep the status line enabled")
	}
	if result.StatusLineRendered || state.LastStatusLineRendered {
		t.Fatal("provider footer rebuilt the native status line")
	}
	if strings.TrimSpace(result.StatusLineText) != "" {
		t.Fatalf("provider footer leaked native text %q", result.StatusLineText)
	}
	// The terminal title is not part of the provider footer and still renders.
	if !result.TerminalTitleRendered {
		t.Fatal("provider footer suppressed the terminal title")
	}

	state.StatusLineProviderActive = false
	result = state.RefreshStatusSurfaces()
	if !result.StatusLineRendered || !strings.Contains(result.StatusLineText, "gpt-5") {
		t.Fatalf("built-in status line did not return: %#v", result)
	}
}

// Rust #61979: read-only history never scans the configured built-in usage
// items; the thread-usage projection lives on the TUI model, so this package
// only verifies the provider flag does not disturb the selection parsing.
func TestStatusControlsSelectionsStayItemBasedWithProviderLikeRust(t *testing.T) {
	selections := NewStatusSurfaceSelections([]string{"thread-credits"}, nil)
	if !selections.UsesThreadUsage() {
		t.Fatal("thread-credits item stopped requesting thread usage")
	}
	state := NewStatusControlsState(StatusControlsRuntime{CWD: "/repo", ModelName: "gpt-5"})
	state.StatusLineProviderActive = true
	if state.usesWorkspaceHeadline(selections) != true {
		t.Fatal("provider did not override the headline decision")
	}
	state.StatusLineProviderActive = false
	if state.usesWorkspaceHeadline(NewStatusSurfaceSelections([]string{"model"}, nil)) {
		t.Fatal("built-in selections gained a workspace headline")
	}
}
