package historycell

import "codex_go/internal/tui"

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
}

func NewProposedPlan(planMarkdown string) ProposedPlanCell {
	return ProposedPlanCell{PlanMarkdown: planMarkdown}
}

func (c ProposedPlanCell) DisplayLines(width int) []string {
	body := rawLinesFromSource(c.PlanMarkdown)
	if len(body) == 0 {
		body = []string{"(empty)"}
	}
	lines := []string{"\u2022 Proposed Plan", " "}
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
