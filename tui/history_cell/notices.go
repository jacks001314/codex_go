package historycell

import (
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/notices.rs.

const (
	SafetyAccessBlockTitle        = "This content can't be shown"
	SafetyAccessBlockLearnMoreURL = "https://help.openai.com/en/articles/20001326"
)

type UpdateAvailableHistoryCell struct {
	CurrentVersion string
	LatestVersion  string
	UpdateCommand  string
}

func NewUpdateAvailable(currentVersion string, latestVersion string, updateCommand string) UpdateAvailableHistoryCell {
	return UpdateAvailableHistoryCell{
		CurrentVersion: strings.TrimSpace(currentVersion),
		LatestVersion:  strings.TrimSpace(latestVersion),
		UpdateCommand:  strings.TrimSpace(updateCommand),
	}
}

func (c UpdateAvailableHistoryCell) DisplayLines(width int) []string {
	lines := c.RawLines()
	if len(lines) == 0 {
		return nil
	}
	innerWidth := max(width-4, 1)
	out := []string{"\u256d" + strings.Repeat("\u2500", innerWidth) + "\u256e"}
	for _, line := range lines {
		wrapped := tui.AdaptiveWrapLine(line, tui.WrapOptions{Width: innerWidth, BreakWords: true})
		for _, item := range wrapped {
			out = append(out, "\u2502 "+padRightRunes(item, innerWidth)+" \u2502")
		}
	}
	out = append(out, "\u2570"+strings.Repeat("\u2500", innerWidth)+"\u256f")
	return out
}

func (c UpdateAvailableHistoryCell) RawLines() []string {
	current := c.CurrentVersion
	if current == "" {
		current = "current"
	}
	latest := c.LatestVersion
	if latest == "" {
		latest = "latest"
	}
	updateInstruction := "See https://github.com/jacks001314/codex_go for installation options."
	if c.UpdateCommand != "" {
		updateInstruction = "Run " + c.UpdateCommand + " to update."
	}
	return []string{
		"Update available!",
		current + " -> " + latest,
		updateInstruction,
		"",
		"See full release notes:",
		"https://github.com/jacks001314/codex_go/releases/latest",
	}
}

func NewWarningEvent(message string) PrefixedWrappedHistoryCell {
	return NewPrefixedWrappedHistoryCell(message, "\u26a0 ", "  ")
}

type SafetyAccessBlockCell struct {
	Title   string
	Body    string
	Actions []SafetyAccessAction
}

// SafetyAccessAction is one labeled link rendered under the block body.
type SafetyAccessAction struct {
	Label string
	URL   string
}

func NewSafetyAccessBlockEvent() SafetyAccessBlockCell {
	return SafetyAccessBlockCell{
		Title: "This content can't be shown",
		Body:  "We take extra caution with requests involving biological research and applications that could pose safety risks. Eligible researchers can apply for Trusted Access.",
		Actions: []SafetyAccessAction{
			{Label: "Trusted Access", URL: "https://chatgpt.com/r/b749fb02595e04c3007a54375f3f4374"},
			{Label: "Learn more", URL: SafetyAccessBlockLearnMoreURL},
		},
	}
}

// NewCyberPolicyErrorEvent renders the Daybreak-aware cyber refusal copy (Rust
// new_cyber_policy_error_event).
func NewCyberPolicyErrorEvent(notice tui.DaybreakNotice) SafetyAccessBlockCell {
	cell := SafetyAccessBlockCell{Title: "This content can\u2019t be shown"}
	switch notice {
	case tui.DaybreakNoticeApply:
		cell.Body = "We take extra care with some cybersecurity requests. If you\u2019re doing authorized security work, apply for Daybreak to get broader access."
		cell.Actions = []SafetyAccessAction{
			{Label: "Learn more", URL: SafetyAccessBlockLearnMoreURL},
			{Label: "Apply for Daybreak", URL: "https://openai.com/form/enterprise-trusted-access-for-cyber/"},
		}
	case tui.DaybreakNoticeAstra:
		cell.Body = "Daybreak isn\u2019t available for Astra. Some cybersecurity requests may still be limited."
		cell.Actions = []SafetyAccessAction{{Label: "Learn more", URL: SafetyAccessBlockLearnMoreURL}}
	default:
		cell.Body = "We take extra care with some cybersecurity requests."
		cell.Actions = []SafetyAccessAction{{Label: "Learn more", URL: SafetyAccessBlockLearnMoreURL}}
	}
	return cell
}

func (c SafetyAccessBlockCell) DisplayLines(width int) []string {
	width = max(width, 1)
	wrapWidth := max(width-2, 1)
	title := c.Title
	if title == "" {
		title = SafetyAccessBlockTitle
	}
	lines := []string{"\u24d8 " + title}
	candidates := []string{"  " + c.Body}
	for _, action := range c.Actions {
		candidates = append(candidates, "  "+action.Label+": "+action.URL)
	}
	for _, line := range candidates {
		lines = append(lines, tui.AdaptiveWrapLine(line, tui.WrapOptions{
			Width:            wrapWidth,
			SubsequentIndent: "  ",
			BreakWords:       true,
		})...)
	}
	return lines
}

func (c SafetyAccessBlockCell) RawLines() []string {
	title := c.Title
	if title == "" {
		title = SafetyAccessBlockTitle
	}
	lines := []string{title, c.Body}
	for _, action := range c.Actions {
		lines = append(lines, action.Label+": "+action.URL)
	}
	return lines
}

type DeprecationNoticeCell struct {
	Summary string
	Details string
}

func NewDeprecationNotice(summary string, details string) DeprecationNoticeCell {
	return DeprecationNoticeCell{Summary: summary, Details: details}
}

func (c DeprecationNoticeCell) DisplayLines(width int) []string {
	width = max(width, 1)
	lines := []string{"\u26a0 " + c.Summary}
	if c.Details != "" {
		lines = append(lines, tui.AdaptiveWrapLine(c.Details, tui.WrapOptions{
			Width:            max(width-4, 1),
			InitialIndent:    "",
			SubsequentIndent: "",
			BreakWords:       true,
		})...)
	}
	return lines
}

func (c DeprecationNoticeCell) RawLines() []string {
	lines := []string{}
	if c.Summary != "" {
		lines = append(lines, c.Summary)
	}
	lines = append(lines, rawLinesFromSource(c.Details)...)
	return lines
}

func NewInfoEvent(message string, hint string) PlainHistoryCell {
	line := "\u2022 " + message
	if hint != "" {
		line += " " + hint
	}
	return NewPlainHistoryCell([]string{line})
}

func NewErrorEvent(message string) PlainHistoryCell {
	return NewPlainHistoryCell([]string{"\u25a0 " + message})
}

func padRightRunes(text string, width int) string {
	if width <= 0 {
		return ""
	}
	text = tui.TruncateToWidth(text, width)
	used := tui.DisplayWidth(text)
	if used >= width {
		return text
	}
	return text + strings.Repeat(" ", width-used)
}
