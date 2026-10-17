package historycell

import (
	"strings"

	"codex_go/tui"
	"codex_go/tui/markdown"
)

// Rust parity: codex-rs/tui/src/history_cell/plans.rs.

type StepStatus int

const (
	StepCompleted StepStatus = iota
	StepInProgress
	StepPending
)

type PlanItemArg struct {
	Step   string
	Status StepStatus
}

type PlanUpdateCell struct {
	Explanation string
	Plan        []PlanItemArg
}

func NewPlanUpdate(explanation string, plan []PlanItemArg) PlanUpdateCell {
	return PlanUpdateCell{Explanation: explanation, Plan: append([]PlanItemArg(nil), plan...)}
}

func (c PlanUpdateCell) DisplayLines(width int) []string {
	lines := []string{"\u2022 Updated Plan"}
	indented := []string{}
	if c.Explanation != "" {
		indented = append(indented, tui.AdaptiveWrapLine(c.Explanation, tui.WrapOptions{
			Width:      max(width-4, 1),
			BreakWords: true,
		})...)
	}
	if len(c.Plan) == 0 {
		indented = append(indented, "(no steps provided)")
	} else {
		for _, item := range c.Plan {
			box := statusGlyph(item.Status)
			indented = append(indented, tui.AdaptiveWrapLine(item.Step, tui.WrapOptions{
				Width:            max(width-4, 1),
				InitialIndent:    box + " ",
				SubsequentIndent: "  ",
				BreakWords:       true,
			})...)
		}
	}
	for _, line := range indented {
		lines = append(lines, "  \u2514 "+line)
	}
	return lines
}

func (c PlanUpdateCell) RawLines() []string {
	lines := []string{"Updated Plan"}
	if c.Explanation != "" {
		lines = append(lines, rawLinesFromSource(c.Explanation)...)
	}
	if len(c.Plan) == 0 {
		lines = append(lines, "(no steps provided)")
		return lines
	}
	for _, item := range c.Plan {
		lines = append(lines, statusName(item.Status)+": "+item.Step)
	}
	return lines
}

type ProposedPlanCell struct {
	PlanMarkdown string
	// ThemeID and CWD select the themed Markdown renderer. When ThemeID is set
	// the plan renders as Markdown on the shaded plan surface (Rust #54315);
	// otherwise the cell keeps the plain fallback.
	ThemeID string
	CWD     string
}

func NewProposedPlan(planMarkdown string) ProposedPlanCell {
	return ProposedPlanCell{PlanMarkdown: planMarkdown}
}

// NewProposedPlanWithTheme builds a proposed-plan cell that renders its
// Markdown through the TUI theme inside the shaded plan surface.
func NewProposedPlanWithTheme(planMarkdown string, themeID string, cwd string) ProposedPlanCell {
	return ProposedPlanCell{PlanMarkdown: planMarkdown, ThemeID: strings.TrimSpace(themeID), CWD: cwd}
}

func (c ProposedPlanCell) DisplayLines(width int) []string {
	lines := []string{"\u2022 Proposed Plan", " "}
	if body, ok := c.renderedBody(width); ok {
		// Rust paints the plan block's padding lines with the plan surface.
		lines = append(lines, markdown.ProposedPlanBlankLine())
		lines = append(lines, body...)
		lines = append(lines, markdown.ProposedPlanBlankLine())
		return lines
	}
	body := rawLinesFromSource(c.PlanMarkdown)
	if len(body) == 0 {
		body = []string{"(empty)"}
	}
	for _, raw := range body {
		lines = append(lines, tui.AdaptiveWrapLine(raw, tui.WrapOptions{
			Width:            max(width-4, 1),
			InitialIndent:    "  ",
			SubsequentIndent: "  ",
			BreakWords:       true,
		})...)
	}
	lines = append(lines, " ")
	return lines
}

// renderedBody renders the plan Markdown at the cell width, indented under the
// bullet like Rust's ProposedPlanCell. It reports false when the themed
// renderer is unavailable so the caller keeps the plain fallback.
func (c ProposedPlanCell) renderedBody(width int) ([]string, bool) {
	if strings.TrimSpace(c.ThemeID) == "" || strings.TrimSpace(c.PlanMarkdown) == "" {
		return nil, false
	}
	rendered, err := markdown.RenderProposedPlanWithThemeCwd(c.PlanMarkdown, max(width-4, 1), c.ThemeID, c.CWD)
	if err != nil || strings.TrimSpace(rendered) == "" {
		return nil, false
	}
	body := strings.Split(strings.Trim(rendered, "\n"), "\n")
	if len(body) == 0 {
		return nil, false
	}
	for index, line := range body {
		body[index] = "  " + line
	}
	return body, true
}

func (c ProposedPlanCell) RawLines() []string {
	return rawLinesFromSource(c.PlanMarkdown)
}

func statusGlyph(status StepStatus) string {
	switch status {
	case StepCompleted:
		return "\u2713"
	case StepInProgress:
		return "\u25b6"
	default:
		return "\u25a1"
	}
}

func statusName(status StepStatus) string {
	switch status {
	case StepCompleted:
		return "Completed"
	case StepInProgress:
		return "InProgress"
	default:
		return "Pending"
	}
}
