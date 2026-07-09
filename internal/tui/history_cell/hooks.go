package historycell

import (
	"strings"
	"unicode"

	"codex_go/internal/tui"
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

func (c HookRunCell) DisplayLines(width int) []string {
	width = max(width, 1)
	lines := tui.AdaptiveWrapLine(c.header(true), tui.WrapOptions{
		Width:            width,
		SubsequentIndent: "  ",
		BreakWords:       true,
	})
	for _, entry := range c.Entries {
		lines = append(lines, wrappedHookEntryLines(entry, width)...)
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
	label := hookEntryLabel(entry.Kind)
	text := strings.TrimRight(entry.Text, "\r\n")
	if text == "" {
		text = strings.TrimSuffix(label, " ")
		label = ""
	}
	out := []string{}
	for index, raw := range strings.Split(text, "\n") {
		initial := "  " + label
		subsequent := "  " + strings.Repeat(" ", len([]rune(label)))
		if index > 0 {
			initial = subsequent
		}
		out = append(out, tui.AdaptiveWrapLine(raw, tui.WrapOptions{
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
	text := strings.TrimRight(entry.Text, "\r\n")
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
