package historycell

import (
	"strings"
	"testing"

	"codex_go/tui"
)

// TestProposedPlanCellRendersShadedMarkdownLikeRust mirrors Rust #54315: a
// themed proposed-plan cell renders its Markdown (table included) on the shaded
// plan surface instead of raw source lines.
func TestProposedPlanCellRendersShadedMarkdownLikeRust(t *testing.T) {
	restoreLevel := tui.SetStdoutColorLevelForTest(tui.ColorTrue)
	defer restoreLevel()
	restore := tui.SetDefaultTerminalColorsForTest(&tui.DefaultColors{
		FG: tui.RGBColor{R: 30, G: 30, B: 30},
		BG: tui.RGBColor{R: 255, G: 255, B: 255},
	})
	defer restore()

	cell := NewProposedPlanWithTheme("## Plan\n\n| Step | Owner |\n| --- | --- |\n| Verify | Codex |\n", "dark", "")
	lines := cell.DisplayLines(80)
	if len(lines) < 4 || !strings.Contains(lines[0], "Proposed Plan") {
		t.Fatalf("lines = %#v", lines)
	}
	if !strings.Contains(lines[2], "48;2;") {
		t.Fatalf("plan padding line is not shaded: %q", lines[2])
	}
	sawBackground := false
	sawTableRow := false
	for _, line := range lines[2:] {
		if strings.Contains(line, "48;2;") {
			sawBackground = true
		}
		if strings.Contains(line, "Verify") {
			sawTableRow = true
			if !strings.Contains(line, "48;2;") {
				t.Fatalf("table row is not shaded: %q", line)
			}
		}
	}
	if !sawBackground || !sawTableRow {
		t.Fatalf("lines = %#v, want shaded markdown table output", lines)
	}

	// The plain constructor keeps the raw fallback (no theme available).
	plain := NewProposedPlan("## Plan").DisplayLines(80)
	for _, line := range plain {
		if strings.Contains(line, "## Plan") {
			return
		}
	}
	t.Fatalf("plain plan lost its raw fallback: %#v", plain)
}
