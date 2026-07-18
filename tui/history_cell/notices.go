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
	updateInstruction := "See https://github.com/openai/codex for installation options."
	if c.UpdateCommand != "" {
		updateInstruction = "Run " + c.UpdateCommand + " to update."
	}
	return []string{
		"Update available!",
		current + " -> " + latest,
		updateInstruction,
		"",
		"See full release notes:",
		"https://github.com/openai/codex/releases/latest",
	}
}

func NewWarningEvent(message string) PrefixedWrappedHistoryCell {
	return NewPrefixedWrappedHistoryCell(message, "\u26a0 ", "  ")
}

type SafetyAccessBlockCell struct {
	Body             string
	TrustedAccessURL string
}

func NewSafetyAccessBlockEvent() SafetyAccessBlockCell {
	return SafetyAccessBlockCell{
		Body:             "We take extra caution with requests involving biological research and applications that could pose safety risks. If you’re a researcher at an approved organization, you may be able to apply for Trusted Access.",
		TrustedAccessURL: "https://openai.com/form/trusted-access-for-life-sciences",
	}
}

func NewCyberPolicyErrorEvent() SafetyAccessBlockCell {
	return SafetyAccessBlockCell{
		Body:             "We take extra caution with cybersecurity requests. If you’re a security professional, you may be able to apply for Trusted Access.",
		TrustedAccessURL: "https://openai.com/form/enterprise-trusted-access-for-cyber/",
	}
}

func (c SafetyAccessBlockCell) DisplayLines(width int) []string {
	width = max(width, 1)
	wrapWidth := max(width-2, 1)
	lines := []string{"\u24d8 " + SafetyAccessBlockTitle}
	for _, line := range []string{
		"  " + c.Body,
		"  Trusted Access: " + c.TrustedAccessURL,
		"  Learn more: " + SafetyAccessBlockLearnMoreURL,
	} {
		lines = append(lines, tui.AdaptiveWrapLine(line, tui.WrapOptions{
			Width:            wrapWidth,
			SubsequentIndent: "  ",
			BreakWords:       true,
		})...)
	}
	return lines
}

func (c SafetyAccessBlockCell) RawLines() []string {
	return []string{
		SafetyAccessBlockTitle,
		c.Body,
		"Trusted Access: " + c.TrustedAccessURL,
		"Learn more: " + SafetyAccessBlockLearnMoreURL,
	}
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
	runes := []rune(text)
	if len(runes) >= width {
		return string(runes[:width])
	}
	return text + strings.Repeat(" ", width-len(runes))
}
