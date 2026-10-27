package historycell

import (
	"strings"

	"codex_go/tui"

	"github.com/rivo/uniseg"
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

type SafetyAccessBlockCell struct {
	Title string
	// Explanation is the optional turn-scoped recourse lead sentence that
	// precedes the body (Rust #57363 SafetyAccessBlockCell.explanation).
	Explanation string
	Body        string
	Actions     []SafetyAccessAction
}

// SafetyAccessAction is one labeled link rendered under the block body.
type SafetyAccessAction struct {
	Label string
	URL   string
}

func NewSafetyAccessBlockEvent() SafetyAccessBlockCell {
	return SafetyAccessBlockCell{
		Title:       "This content can't be shown",
		Explanation: "We take extra caution with requests involving biological research and applications that could pose safety risks.",
		Body:        "Eligible researchers can apply for Trusted Access.",
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
		cell.Explanation = "We take extra care with some cybersecurity requests."
		cell.Body = "If you\u2019re doing authorized security work, apply for Daybreak to get broader access."
		cell.Actions = []SafetyAccessAction{
			{Label: "Learn more", URL: SafetyAccessBlockLearnMoreURL},
			{Label: "Apply for Daybreak", URL: "https://openai.com/form/enterprise-trusted-access-for-cyber/"},
		}
	case tui.DaybreakNoticeAstra:
		cell.Body = "Daybreak isn\u2019t available for Astra. Some cybersecurity requests may still be limited."
		cell.Actions = []SafetyAccessAction{{Label: "Learn more", URL: SafetyAccessBlockLearnMoreURL}}
	case tui.DaybreakNoticeDisabled:
		cell.Body = "Daybreak is currently off. If you\u2019re doing authorized security work, turn it on and try your request again."
		cell.Actions = []SafetyAccessAction{{Label: "Learn more", URL: SafetyAccessBlockLearnMoreURL}}
	case tui.DaybreakNoticeEnabled:
		cell.Body = "Some cybersecurity requests are still limited, even when Daybreak is on."
		cell.Actions = []SafetyAccessAction{{Label: "Learn more", URL: SafetyAccessBlockLearnMoreURL}}
	default:
		cell.Body = "We take extra care with some cybersecurity requests."
		cell.Actions = []SafetyAccessAction{{Label: "Learn more", URL: SafetyAccessBlockLearnMoreURL}}
	}
	return cell
}

// Message renders the explanation and body as the single paragraph shown to the
// user (Rust SafetyAccessBlockCell::message).
func (c SafetyAccessBlockCell) Message() string {
	if explanation := strings.TrimSpace(c.Explanation); explanation != "" {
		return explanation + " " + c.Body
	}
	return c.Body
}

func (c SafetyAccessBlockCell) DisplayLines(width int) []string {
	width = max(width, 1)
	wrapWidth := max(width-2, 1)
	title := c.Title
	if title == "" {
		title = SafetyAccessBlockTitle
	}
	lines := []string{"\u24d8 " + title}
	// Rust wraps `"  " + message` at `width - 2` with a two-column hanging
	// indent, so wrapping the bare message at `width - 4` and prefixing two
	// spaces reproduces both the first-row and continuation widths.
	for _, line := range tui.AdaptiveWrapLine(c.Message(), tui.WrapOptions{
		Width:      max(wrapWidth-2, 1),
		BreakWords: true,
	}) {
		lines = append(lines, "  "+line)
	}
	for _, action := range c.Actions {
		// Rust uses the non-adaptive `word_wrap_line` for action rows, which
		// keeps the standard textwrap break opportunities (hyphens and
		// slashes) instead of preserving the URL as one token.
		lines = append(lines, wrapSafetyAccessActionLine(
			"  "+action.Label+": "+action.URL,
			wrapWidth,
			"  ",
		)...)
	}
	return lines
}

// safetyAccessWrapUnit is a textwrap-style word plus its trailing whitespace.
type safetyAccessWrapUnit struct {
	word       string
	whitespace string
}

// wrapSafetyAccessActionLine mirrors Rust's standard `word_wrap_line` call for
// safety action rows: textwrap FirstFit with hyphen/slash break opportunities,
// a hanging indent, and the caller's leading spaces kept on the first row.
func wrapSafetyAccessActionLine(text string, width int, indent string) []string {
	width = max(width, 1)
	indentWidth := tui.DisplayWidth(indent)
	var lines []string
	line := ""
	lineWidth := 0
	atLineStart := true
	for _, unit := range safetyAccessWrapUnits(text) {
		wordWidth := tui.DisplayWidth(unit.word)
		spaceWidth := tui.DisplayWidth(unit.whitespace)
		if !atLineStart && lineWidth+wordWidth+spaceWidth > width {
			lines = append(lines, strings.TrimRight(line, " \t"))
			line = indent
			lineWidth = indentWidth
			atLineStart = true
		}
		if lineWidth+wordWidth > width {
			// textwrap breaks an overlong word by width when no break
			// opportunity can make progress.
			available := max(width-lineWidth, 1)
			for index, piece := range splitSafetyAccessWordByWidth(unit.word, available) {
				if index > 0 {
					lines = append(lines, strings.TrimRight(line, " \t"))
					line = indent
					lineWidth = indentWidth
					available = max(width-lineWidth, 1)
				}
				line += piece
				lineWidth += tui.DisplayWidth(piece)
			}
		} else {
			line += unit.word
			lineWidth += wordWidth
		}
		line += unit.whitespace
		lineWidth += spaceWidth
		atLineStart = false
	}
	return append(lines, strings.TrimRight(line, " \t"))
}

func safetyAccessWrapUnits(text string) []safetyAccessWrapUnit {
	runes := []rune(text)
	var units []safetyAccessWrapUnit
	index := 0
	start := index
	for index < len(runes) && isSafetyAccessWrapSpace(runes[index]) {
		index++
	}
	if index > start {
		// Leading whitespace is a zero-width word in textwrap, which keeps it
		// on the first row instead of dropping it.
		units = append(units, safetyAccessWrapUnit{whitespace: string(runes[start:index])})
	}
	for index < len(runes) {
		start = index
		for index < len(runes) && !isSafetyAccessWrapSpace(runes[index]) {
			index++
		}
		word := string(runes[start:index])
		spaceStart := index
		for index < len(runes) && isSafetyAccessWrapSpace(runes[index]) {
			index++
		}
		whitespace := string(runes[spaceStart:index])
		pieces := splitSafetyAccessBreakOpportunities(word)
		for pieceIndex, piece := range pieces {
			if pieceIndex == len(pieces)-1 {
				units = append(units, safetyAccessWrapUnit{word: piece, whitespace: whitespace})
			} else {
				units = append(units, safetyAccessWrapUnit{word: piece})
			}
		}
	}
	return units
}

func isSafetyAccessWrapSpace(r rune) bool {
	switch r {
	case '\u00a0', '\u202f', '\u2007':
		return false
	}
	return r == ' ' || r == '\t'
}

// splitSafetyAccessBreakOpportunities approximates textwrap's default Unicode
// line-breaking opportunities: hyphens and slashes end a breakable piece.
func splitSafetyAccessBreakOpportunities(word string) []string {
	var pieces []string
	start := 0
	for index, r := range word {
		if r != '-' && r != '/' {
			continue
		}
		end := index + len(string(r))
		pieces = append(pieces, word[start:end])
		start = end
	}
	if start < len(word) || len(pieces) == 0 {
		pieces = append(pieces, word[start:])
	}
	return pieces
}

func splitSafetyAccessWordByWidth(word string, width int) []string {
	width = max(width, 1)
	var pieces []string
	var current strings.Builder
	used := 0
	graphemes := uniseg.NewGraphemes(word)
	for graphemes.Next() {
		grapheme := graphemes.Str()
		graphemeWidth := tui.DisplayWidth(grapheme)
		if used > 0 && used+graphemeWidth > width {
			pieces = append(pieces, current.String())
			current.Reset()
			used = 0
		}
		current.WriteString(grapheme)
		used += graphemeWidth
	}
	if current.Len() > 0 || len(pieces) == 0 {
		pieces = append(pieces, current.String())
	}
	return pieces
}

func (c SafetyAccessBlockCell) RawLines() []string {
	title := c.Title
	if title == "" {
		title = SafetyAccessBlockTitle
	}
	lines := []string{title, c.Message()}
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
