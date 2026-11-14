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

	rendered := model.renderDimmedPaneStatus("alpha beta gamma delta epsilon zeta", 12)
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

	content := model.renderSidePaneTranscript(&transcriptMessageCache{}, "thread-side", nil, 20, "side status")
	if !strings.Contains(content, "side status") {
		t.Fatalf("pane transcript missing status: %q", content)
	}
	if content := model.renderSidePaneTranscript(&transcriptMessageCache{}, "thread-side", nil, 20, ""); strings.Contains(content, "side status") {
		t.Fatalf("focused pane rendered status: %q", content)
	}
}
