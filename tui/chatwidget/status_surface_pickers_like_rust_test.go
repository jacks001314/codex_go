package chatwidget

import (
	"strings"
	"testing"

	"codex_go/tui/bottom_pane"
)

// TestStatusSurfacePickersShareConfiguredOrderingLikeRust mirrors Rust #62126:
// both the status line and terminal title pickers build their rows with one
// helper, so configured rows keep their order, a repeated row collapses to one
// canonical entry at its first position, and the remaining rows follow in enum
// order.
func TestStatusSurfacePickersShareConfiguredOrderingLikeRust(t *testing.T) {
	statusLine := NewStatusLineSetupView([]bottompane.StatusLineItem{
		bottompane.StatusLineProjectRoot,
		bottompane.StatusLineModelName,
		bottompane.StatusLineProjectRoot,
	}, true, DefaultStatusSurfacePreviewData())
	// Row 0 is the theme-colors toggle, which Rust pushes before the items.
	if statusLine.Items[0].ID != StatusLineUseThemeColorsItemID {
		t.Fatalf("first row = %q, want the theme-colors toggle", statusLine.Items[0].ID)
	}
	got := []string{}
	for _, item := range statusLine.Items[1:] {
		got = append(got, item.ID)
	}
	if len(got) < 3 || got[0] != "project-name" || got[1] != "model" || got[2] != "model-with-reasoning" {
		t.Fatalf("status line order = %#v, want the configured rows first", got[:min(3, len(got))])
	}
	if countID(got, "project-name") != 1 {
		t.Fatalf("status line rows = %#v, want one project-name row", got)
	}
	if countID(got, "model") != 1 {
		t.Fatalf("status line rows = %#v, want one model row", got)
	}
	// Rows the configuration did not pick keep enum order after it.
	remaining := StatusLineItemIDs(AllStatusLineItems())
	remaining = withoutIDs(remaining, "project-name", "model")
	if tail := got[2:]; !equalIDs(tail, remaining) {
		t.Fatalf("status line tail = %#v, want enum order %#v", tail, remaining)
	}

	title := NewTerminalTitleSetupView([]TerminalTitleItem{
		TerminalTitleProject,
		TerminalTitleAppName,
		TerminalTitleProject,
	}, DefaultStatusSurfacePreviewData())
	titleIDs := []string{}
	for _, item := range title.Items {
		titleIDs = append(titleIDs, item.ID)
	}
	if len(titleIDs) < 3 || titleIDs[0] != "project-name" || titleIDs[1] != "app-name" || titleIDs[2] != "current-dir" {
		t.Fatalf("terminal title order = %#v, want the configured rows first", titleIDs[:min(3, len(titleIDs))])
	}
	if countID(titleIDs, "project-name") != 1 {
		t.Fatalf("terminal title rows = %#v, want one project-name row", titleIDs)
	}
	titleRemaining := terminalTitleIDs(AllTerminalTitleItems())
	titleRemaining = withoutIDs(titleRemaining, "project-name", "app-name")
	if tail := titleIDs[2:]; !equalIDs(tail, titleRemaining) {
		t.Fatalf("terminal title tail = %#v, want enum order %#v", tail, titleRemaining)
	}

	// Both pickers keep every canonical row exactly once whatever was selected.
	if len(statusLine.Items)-1 != len(AllStatusLineItems()) {
		t.Fatalf("status line rows = %d, want %d", len(statusLine.Items)-1, len(AllStatusLineItems()))
	}
	if len(title.Items) != len(AllTerminalTitleItems()) {
		t.Fatalf("terminal title rows = %d, want %d", len(title.Items), len(AllTerminalTitleItems()))
	}
}

// TestStatusSurfacePickersUseLiveRateLimitCopyLikeRust keeps the live usage
// limit name and description in both pickers instead of the static defaults: a
// live "usage 10% left" reads as the generic usage limit rather than the
// five-hour one.
func TestStatusSurfacePickersUseLiveRateLimitCopyLikeRust(t *testing.T) {
	previewData := DefaultStatusSurfacePreviewData()
	previewData.SetLive(StatusPreviewFiveHourLimit, "\u00a0usage 10% left")
	previewData.SetLive(StatusPreviewWeeklyLimit, "\u00a0weekly 10% left")

	statusLine := NewStatusLineSetupView([]bottompane.StatusLineItem{
		bottompane.StatusLineFiveHourLimit,
		bottompane.StatusLineWeeklyLimit,
	}, true, previewData)
	primary := findStatusLineRow(t, statusLine, "five-hour-limit")
	if primary.Name != "usage-limit" {
		t.Fatalf("status line primary name = %q, want the live name", primary.Name)
	}
	if primary.Description != "Remaining usage on the primary usage limit (omitted when unavailable)" {
		t.Fatalf("status line primary description = %q, want the live copy", primary.Description)
	}
	weekly := findStatusLineRow(t, statusLine, "weekly-limit")
	if weekly.Name != "weekly-limit" || !strings.Contains(weekly.Description, "weekly usage limit") {
		t.Fatalf("status line weekly row = %#v, want the live copy", weekly)
	}

	title := NewTerminalTitleSetupView([]TerminalTitleItem{
		TerminalTitleFiveHourLimit,
		TerminalTitleWeeklyLimit,
	}, previewData)
	if row := findTitleRow(t, title, "five-hour-limit"); row.Name != "usage-limit" ||
		row.Description != "Remaining usage on the primary usage limit (omitted when unavailable)" {
		t.Fatalf("terminal title primary row = %#v, want the live copy", row)
	}
	if row := findTitleRow(t, title, "weekly-limit"); row.Name != "weekly-limit" ||
		!strings.Contains(row.Description, "weekly usage limit") {
		t.Fatalf("terminal title weekly row = %#v, want the live copy", row)
	}

	// Without a live value the static copy stands, so the live rows above are
	// what changed the picker rather than the fallback text.
	staticLine := NewStatusLineSetupView([]bottompane.StatusLineItem{bottompane.StatusLineFiveHourLimit}, true, DefaultStatusSurfacePreviewData())
	if row := findStatusLineRow(t, staticLine, "five-hour-limit"); row.Name != "five-hour-limit" ||
		!strings.Contains(row.Description, "primary usage limit") {
		t.Fatalf("static status line row = %#v, want the default copy", row)
	}
	staticTitle := NewTerminalTitleSetupView([]TerminalTitleItem{TerminalTitleWeeklyLimit}, DefaultStatusSurfacePreviewData())
	if row := findTitleRow(t, staticTitle, "weekly-limit"); row.Name != "weekly-limit" ||
		!strings.Contains(row.Description, "secondary usage limit") {
		t.Fatalf("static terminal title row = %#v, want the default copy", row)
	}
}

func findTitleRow(t *testing.T, view TerminalTitleSetupView, id string) TerminalTitleSetupItem {
	t.Helper()
	for _, item := range view.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("terminal title picker is missing %q", id)
	return TerminalTitleSetupItem{}
}

func countID(ids []string, want string) int {
	count := 0
	for _, id := range ids {
		if id == want {
			count++
		}
	}
	return count
}

func terminalTitleIDs(items []TerminalTitleItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID())
	}
	return ids
}

func withoutIDs(ids []string, drop ...string) []string {
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		skip := false
		for _, want := range drop {
			if id == want {
				skip = true
				break
			}
		}
		if !skip {
			kept = append(kept, id)
		}
	}
	return kept
}

func equalIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func findStatusLineRow(t *testing.T, view StatusLineSetupView, id string) StatusLineSetupItem {
	t.Helper()
	for _, item := range view.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("status line picker is missing %q", id)
	return StatusLineSetupItem{}
}
