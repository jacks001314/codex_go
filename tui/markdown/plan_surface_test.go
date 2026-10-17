package markdown

import (
	"regexp"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

var planFGSGR = regexp.MustCompile("\\x1b\\[38;2;([0-9]+);([0-9]+);([0-9]+)m")

// TestApplyPlanSurfacePaintsFillAndAdaptsForegrounds mirrors Rust #54315's
// `style_proposed_plan_line`: the plan fill covers every layout line, explicit
// foregrounds are re-resolved against that fill, and a reset re-asserts the
// fill so inner spans cannot punch holes in the block.
func TestApplyPlanSurfacePaintsFillAndAdaptsForegrounds(t *testing.T) {
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorTrue)
	defer restoreLevel()
	background := codextui.RGB{R: 244, G: 244, B: 244}
	surface := codextui.TerminalColor{RGB: &background}
	level := codextui.ColorTrue
	// A near-white foreground has less than the 4.5 minimum contrast on the
	// light plan fill, so it must be pushed darker.
	line := "\x1b[38;2;200;200;200mtext\x1b[0m"
	styled := applyPlanSurface(line, surface, background, level)
	if !strings.HasPrefix(styled, "\x1b[48;2;244;244;244m") {
		t.Fatalf("styled line = %q, want the plan fill prefix", styled)
	}
	matches := planFGSGR.FindAllStringSubmatch(styled, -1)
	if len(matches) == 0 {
		t.Fatalf("styled line = %q, want an explicit foreground", styled)
	}
	if matches[0][1] == "200" && matches[0][2] == "200" && matches[0][3] == "200" {
		t.Fatalf("foreground was not adapted to the plan fill: %q", styled)
	}
	if !strings.Contains(styled, "\x1b[0;48;2;244;244;244m") {
		t.Fatalf("reset did not re-assert the fill: %q", styled)
	}
	if regexp.MustCompile("\\x1b\\[[0-9;]*\\x1b").MatchString(styled) {
		t.Fatalf("nested ESC inside SGR parameters: %q", styled)
	}

	blank := applyPlanSurface("\n", surface, background, level)
	for _, candidate := range strings.Split(blank, "\n") {
		if candidate == "" {
			continue
		}
		if !strings.HasPrefix(candidate, "\x1b[48;2;244;244;244m") {
			t.Fatalf("blank layout line is not shaded: %q", candidate)
		}
	}
}

// TestRenderProposedPlanWithThemeCwdShadesMarkdown pins the cell-facing entry
// point: Markdown structure is preserved (table separators) and every line
// carries the plan fill.
func TestRenderProposedPlanWithThemeCwdShadesMarkdown(t *testing.T) {
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorTrue)
	defer restoreLevel()
	restore := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 30, G: 30, B: 30},
		BG: codextui.RGBColor{R: 255, G: 255, B: 255},
	})
	defer restore()

	rendered, err := RenderProposedPlanWithThemeCwd("## Plan\n\n| Step | Owner |\n| --- | --- |\n| Verify | Codex |\n", 60, "dark", "")
	if err != nil {
		t.Fatalf("RenderProposedPlanWithThemeCwd() error = %v", err)
	}
	if !strings.Contains(rendered, "Step") || !strings.Contains(rendered, "Verify") {
		t.Fatalf("rendered plan = %q, want the table content", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if !strings.HasPrefix(line, "\x1b[48;2;") {
			t.Fatalf("line is not shaded with the plan fill: %q", line)
		}
	}
}
