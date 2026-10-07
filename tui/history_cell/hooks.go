package historycell

import (
	"strings"
	"unicode"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/chatwidget/hook_lifecycle.rs.

type HookOutputKind string

const (
	HookOutputWarning  HookOutputKind = "warning"
	HookOutputStop     HookOutputKind = "stop"
	HookOutputFeedback HookOutputKind = "feedback"
	HookOutputContext  HookOutputKind = "context"
	HookOutputError    HookOutputKind = "error"
)

type HookOutputEntry struct {
	Kind HookOutputKind
	Text string
}

type HookRunCell struct {
	EventName     string
	Status        string
	StatusMessage string
	Entries       []HookOutputEntry
	Running       bool
}

func NewRunningHookRun(eventName string, statusMessage string) HookRunCell {
	return HookRunCell{
		EventName:     strings.TrimSpace(eventName),
		Status:        "running",
		StatusMessage: strings.TrimSpace(statusMessage),
		Running:       true,
	}
}

func NewHookRun(eventName string, status string, statusMessage string, entries []HookOutputEntry) HookRunCell {
	return HookRunCell{
		EventName:     strings.TrimSpace(eventName),
		Status:        strings.TrimSpace(status),
		StatusMessage: strings.TrimSpace(statusMessage),
		Entries:       append([]HookOutputEntry(nil), entries...),
	}
}

// DisplayLines renders the hook cell as plain text. Hook system messages carry
// ANSI styles, so the plain projection is the escape-stripped form of the
// styled projection (Rust HookCell::display_lines + raw_lines).
func (c HookRunCell) DisplayLines(width int) []string {
	return PlainLines(c.DisplayStyledLines(width))
}

// DisplayStyledLines renders the hook cell with ANSI styles parsed into spans,
// so colours and text styles survive across lines instead of leaking raw
// escape bytes. Rust parity: HookCell::display_lines in hook_cell.rs.
func (c HookRunCell) DisplayStyledLines(width int) []StyledLine {
	width = max(width, 1)
	lines := WrapStyledLine(c.header(true), tui.WrapOptions{
		Width:            width,
		SubsequentIndent: "  ",
		BreakWords:       true,
	})
	for _, entry := range c.Entries {
		lines = append(lines, styledHookEntryLines(entry, width)...)
	}
	return lines
}

func (c HookRunCell) RawLines() []string {
	lines := []string{c.header(false)}
	for _, entry := range c.Entries {
		lines = append(lines, rawHookEntryLines(entry)...)
	}
	return lines
}

func (c HookRunCell) header(display bool) string {
	prefix := ""
	if display {
		prefix = "\u2022 "
	}
	eventName := HookEventDisplayName(c.EventName)
	if c.Running || strings.EqualFold(c.Status, "running") {
		header := prefix + "Running " + eventName + " hook"
		if c.StatusMessage != "" {
			header += ": " + c.StatusMessage
		}
		return header
	}
	status := strings.TrimSpace(c.Status)
	if status == "" {
		status = "completed"
	}
	return prefix + eventName + " hook (" + status + ")"
}

func HookEventDisplayName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Hook"
	}
	runes := []rune(name)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func wrappedHookEntryLines(entry HookOutputEntry, width int) []string {
	return PlainLines(styledHookEntryLines(entry, width))
}

// styledHookEntryLines keeps the existing hook-entry shape (label prefix on the
// first line, aligned continuation indent afterwards) while parsing ANSI styles
// out of the entry text. Styles are parsed before the text is split so a style
// opened on one line still applies on the next, and a text that ends with a
// newline keeps its trailing blank line (Rust hook_cell.rs).
func styledHookEntryLines(entry HookOutputEntry, width int) []StyledLine {
	label := hookEntryLabel(entry.Kind)
	text := strings.ReplaceAll(entry.Text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	trailingBlank := strings.HasSuffix(text, "\n")
	text = strings.TrimRight(text, "\n")
	spans := ParseANSISpans(text)
	if SpanText(spans) == "" {
		spans = ParseANSISpans(strings.TrimSuffix(label, " "))
		label = ""
	}
	entryLines := StyledLinesFromSpans(spans)
	if trailingBlank {
		entryLines = append(entryLines, StyledLine{})
	}
	out := []StyledLine{}
	for index, line := range entryLines {
		initial := "  " + label
		subsequent := "  " + strings.Repeat(" ", len([]rune(label)))
		if index > 0 {
			initial = subsequent
		}
		if len(line) == 0 {
			out = append(out, StyledLine{})
			continue
		}
		out = append(out, WrapStyledSpans(line, tui.WrapOptions{
			Width:            width,
			InitialIndent:    initial,
			SubsequentIndent: subsequent,
			BreakWords:       true,
		})...)
	}
	return out
}

func rawHookEntryLines(entry HookOutputEntry) []string {
	label := strings.TrimSpace(hookEntryLabel(entry.Kind))
	// Hook system messages may carry ANSI styles; raw output keeps the text but
	// never leaks escape sequences.
	text := strings.TrimRight(PlainTextFromANSI(entry.Text), "\r\n")
	if text == "" {
		return []string{label}
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = label + " " + lines[i]
			continue
		}
		lines[i] = strings.Repeat(" ", len([]rune(label))+1) + lines[i]
	}
	return lines
}

func hookEntryLabel(kind HookOutputKind) string {
	switch kind {
	case HookOutputWarning:
		return "warning: "
	case HookOutputStop:
		return "stop: "
	case HookOutputFeedback:
		return "feedback: "
	case HookOutputContext:
		return "hook context: "
	case HookOutputError:
		return "error: "
	default:
		return "hook output: "
	}
}
