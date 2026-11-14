package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust #61141: inactive side panes keep their own status content, wrapped to
// the pane and dimmed, while the focused pane relies on the composer status.
func TestSidePaneStatusContentLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.paneStatusText = map[string]string{
		"thread-parent": "model gpt-5 readonly cwd /repo",
		"thread-side":   "side status",
	}

	if got := model.paneStatusForThread("thread-parent"); got != "model gpt-5 readonly cwd /repo" {
		t.Fatalf("cached status = %q", got)
	}
	if got := model.paneStatusWhenUnfocused(false, "thread-parent"); got != "" {
		t.Fatalf("focused pane status = %q, want empty", got)
	}
	if got := model.paneStatusWhenUnfocused(true, "thread-side"); got != "side status" {
		t.Fatalf("unfocused pane status = %q", got)
	}

	rendered := model.renderDimmedPaneStatus("thread-parent", "alpha beta gamma delta epsilon zeta", 12, 3)
	rows := strings.Split(rendered, "\n")
	if len(rows) < 2 || len(rows) > 3 {
		t.Fatalf("wrapped rows = %d (%q)", len(rows), rendered)
	}
	for _, row := range rows {
		if !strings.HasPrefix(row, "\x1b[2m") || !strings.HasSuffix(row, "\x1b[0m") {
			t.Fatalf("row is not dimmed: %q", row)
		}
		if width := codextui.DisplayWidth(strings.TrimSuffix(strings.TrimPrefix(row, "\x1b[2m"), "\x1b[0m")); width > 12 {
			t.Fatalf("row width = %d, want <= 12: %q", width, row)
		}
	}

	content := model.renderSidePaneTranscript(&transcriptMessageCache{}, "thread-side", nil, 20, "side status", 3)
	if !strings.Contains(content, "side status") {
		t.Fatalf("pane transcript missing status: %q", content)
	}
	if content := model.renderSidePaneTranscript(&transcriptMessageCache{}, "thread-side", nil, 20, "", 3); strings.Contains(content, "side status") {
		t.Fatalf("focused pane rendered status: %q", content)
	}
}

// Rust #61141: status rows scale with the pane height while the transcript
// keeps at least one visible row.
func TestSidePaneStatusRowBudgetLikeRust(t *testing.T) {
	for _, tc := range []struct {
		height int
		want   int
	}{
		{0, 0}, {1, 0}, {2, 0}, {6, 1}, {12, 1}, {20, 2}, {24, 3}, {40, 3},
	} {
		if got := paneStatusRowBudget(tc.height); got != tc.want {
			t.Fatalf("paneStatusRowBudget(%d) = %d, want %d", tc.height, got, tc.want)
		}
	}
}

// A themed status that fits the pane keeps its colour under the dim wrapper.
func TestSidePaneStatusKeepsThemeWhenItFitsLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.paneStatusText = map[string]string{"thread-side": "themed status"}
	model.paneStatusStyled = map[string]string{"thread-side": "\x1b[36mthemed\x1b[0m status"}

	rendered := model.renderDimmedPaneStatus("thread-side", "themed status", 40, 3)
	if !strings.Contains(rendered, "\x1b[36m") {
		t.Fatalf("styled status lost its colour: %q", rendered)
	}
	if !strings.HasPrefix(rendered, "\x1b[2m") || !strings.HasSuffix(rendered, "\x1b[0m") {
		t.Fatalf("styled status is not dimmed: %q", rendered)
	}

	narrow := model.renderDimmedPaneStatus("thread-side", "themed status", 8, 3)
	if strings.Contains(narrow, "\x1b[36m") {
		t.Fatalf("narrow pane reused an overflowing styled line: %q", narrow)
	}
}

// Structured status content reflows into multiple rows without losing colours.
func TestSidePaneStatusReflowsWithThemeLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	spans := []paneStatusSpan{
		{text: "model ", sgr: ""},
		{text: "gpt-5 ", sgr: "\x1b[36m"},
		{text: "readonly cwd /repo", sgr: ""},
	}
	model.paneStatusSpans = map[string][]paneStatusSpan{"thread-side": spans}
	model.paneStatusText = map[string]string{"thread-side": "model gpt-5 readonly cwd /repo"}

	rendered := model.renderDimmedPaneStatus("thread-side", "model gpt-5 readonly cwd /repo", 12, 4)
	rows := strings.Split(rendered, "\n")
	if len(rows) < 2 {
		t.Fatalf("status did not reflow: %q", rendered)
	}
	for _, row := range rows {
		if !strings.HasPrefix(row, "\x1b[2m") || !strings.HasSuffix(row, "\x1b[0m") {
			t.Fatalf("row is not dimmed: %q", row)
		}
	}
	coloured := false
	for _, row := range rows {
		if strings.Contains(row, "\x1b[36m") {
			coloured = true
		}
		if width := codextui.DisplayWidth(row); width > 12 {
			t.Fatalf("row width = %d, want <= 12: %q", width, row)
		}
	}
	if !coloured {
		t.Fatalf("reflowed status lost its colour: %q", rendered)
	}
}
