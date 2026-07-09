package chatwidget

import (
	"strings"
	"testing"

	codextui "codex_go/internal/tui"
)

func TestAutoReviewDenialsPopupOutcomesRowsAndSelectionMatchRust(t *testing.T) {
	empty := BuildAutoReviewDenialsPopup("thread-1", nil)
	if empty.Outcome != AutoReviewDenialsPopupInfo || empty.Message != "No recent auto-review denials in this thread." {
		t.Fatalf("empty outcome = %#v", empty)
	}
	if !strings.Contains(empty.Hint, "auto-review rejects") {
		t.Fatalf("empty hint = %q", empty.Hint)
	}

	missingThread := BuildAutoReviewDenialsPopup("", []AutoReviewDenialEntry{{ID: "denial-1", Summary: "cmd"}})
	if missingThread.Outcome != AutoReviewDenialsPopupError || missingThread.Message != "That thread is no longer available." {
		t.Fatalf("missing thread outcome = %#v", missingThread)
	}

	result := BuildAutoReviewDenialsPopup("thread-1", []AutoReviewDenialEntry{
		{ID: "denial-1", Summary: "$ rm -rf build", Rationale: "Writes outside the workspace."},
		{ID: "denial-2", Summary: "$ curl example.com"},
	})
	if result.Outcome != AutoReviewDenialsPopupShow {
		t.Fatalf("outcome = %s, want show", result.Outcome)
	}
	view := result.View
	if view.ViewID != AutoReviewDenialsPopupViewID || view.Title != "Auto-review Denials" || view.Subtitle != "Select a denied action to approve." {
		t.Fatalf("view metadata = %#v", view)
	}
	if !view.Searchable || view.SearchPlaceholder != "Type to search denials" || view.InitialSelectedIndex != 1 {
		t.Fatalf("search/selection = %#v", view)
	}
	if len(view.Items) != 3 || !view.Items[0].Disabled || view.Items[0].Name != "Command" || view.Items[0].Description != "Rationale" {
		t.Fatalf("header item = %#v", view.Items)
	}
	if view.Items[1].Action != PermissionActionApproveRecentAutoReviewDenial || !view.Items[1].DismissOnSelect {
		t.Fatalf("approval action = %#v", view.Items[1])
	}
	if view.Items[2].Description != "Auto-review did not include a rationale." {
		t.Fatalf("fallback rationale = %q", view.Items[2].Description)
	}

	rows := SelectionViewRows(view, -1, 120)
	wantSelected := codextui.RenderSelectedRow("2. $ rm -rf build - Writes outside the workspace.")
	if !containsChatwidgetRow(rows, wantSelected) {
		t.Fatalf("selected row missing:\n%s", strings.Join(rows, "\n"))
	}
	if containsChatwidgetRow(rows, codextui.RenderSelectedRow("1. Command - Rationale (disabled)")) {
		t.Fatalf("disabled header should not be selected:\n%s", strings.Join(rows, "\n"))
	}
}

func TestAutoReviewDenialEntriesFromSummariesAndApproveMatchRust(t *testing.T) {
	entries := AutoReviewDenialEntriesFromSummaries([]codextui.AutoReviewDenial{
		{ID: " one ", Summary: " command one "},
		{},
		{ID: "two"},
	})
	if len(entries) != 2 || entries[0].ID != "one" || entries[0].Summary != "command one" || entries[1].ID != "two" {
		t.Fatalf("entries = %#v", entries)
	}

	state := AutoReviewDenialsState{Entries: []AutoReviewDenialEntry{
		{ID: "one", Summary: "command one", Rationale: "blocked"},
		{ID: "two", Summary: "command two"},
	}}
	result := ApproveRecentAutoReviewDenial(&state, "one")
	if !result.Approved || result.Entry.ID != "one" || len(state.Entries) != 1 || state.Entries[0].ID != "two" {
		t.Fatalf("approve result=%#v state=%#v", result, state)
	}
	if result.Message != "Approval recorded for one retry of the selected auto-review denial." {
		t.Fatalf("message = %q", result.Message)
	}
	if !strings.Contains(result.Hint, "retry still goes through auto-review") {
		t.Fatalf("hint = %q", result.Hint)
	}

	missing := ApproveRecentAutoReviewDenial(&state, "missing")
	if missing.Approved || missing.Error != "That auto-review denial is no longer available." {
		t.Fatalf("missing result = %#v", missing)
	}
}

func TestFullAccessConfirmationHeaderAndSelectedRowsMatchRust(t *testing.T) {
	view := FullAccessConfirmationView()
	if len(view.HeaderLines) != 2 || view.HeaderLines[0] != "Enable full access?" {
		t.Fatalf("header = %#v", view.HeaderLines)
	}
	if !strings.Contains(view.HeaderLines[1], "without your approval") || !strings.Contains(view.HeaderLines[1], "risk of data loss") {
		t.Fatalf("warning = %q", view.HeaderLines[1])
	}

	rows := PermissionMenuViewRows(view, -1, 160)
	wantSelected := codextui.RenderSelectedRow("1. Yes, continue anyway - Apply full access for this session")
	if !containsChatwidgetRow(rows, wantSelected) {
		t.Fatalf("selected full-access row missing:\n%s", strings.Join(rows, "\n"))
	}
	if !containsChatwidgetRow(rows, "Enable full access?") || !containsChatwidgetRow(rows, standardPopupHintLine) {
		t.Fatalf("rows missing header/footer:\n%s", strings.Join(rows, "\n"))
	}
}

func TestSelectionViewRowsTruncateByDisplayWidth(t *testing.T) {
	view := SelectionView{
		Title: "Wide",
		Items: []SelectionItem{{
			ID:          "wide",
			Name:        "中文项目名称很长",
			Description: "描述很长",
		}},
	}
	rows := SelectionViewRows(view, 0, 16)
	if len(rows) < 2 {
		t.Fatalf("rows = %#v", rows)
	}
	row := stripChatwidgetANSI(rows[1])
	if width := codextui.DisplayWidth(row); width > 16 {
		t.Fatalf("row exceeds width: %q width=%d rows=%#v", row, width, rows)
	}
	if !strings.Contains(row, "\u2026") || strings.Contains(row, "...") {
		t.Fatalf("row should use Rust ellipsis truncation: %q", row)
	}
}

func containsChatwidgetRow(rows []string, want string) bool {
	for _, row := range rows {
		if row == want || strings.Contains(row, want) {
			return true
		}
	}
	return false
}

func stripChatwidgetANSI(value string) string {
	return strings.NewReplacer(
		"\x1b[1;94m", "",
		"\x1b[94;1m", "",
		"\x1b[0m", "",
		"\x1b[1m", "",
		"\x1b[94m", "",
	).Replace(value)
}
