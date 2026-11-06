package historycell

import (
	"strings"

	"codex_go/context"
	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/delegated.rs (#59789).

// DelegatedPromptCell renders an incoming delegated message. The source task
// attribution stays literal while the message body renders as Markdown so
// formatting, lists and hyperlinks survive instead of showing raw markup.
type DelegatedPromptCell struct {
	SourceThreadID string
	Prompt         string
}

// NewDelegatedPromptCell mirrors the Rust replay/transcript cell for a delegated
// tool output, keeping the `Sent by Codex from task …` attribution.
func NewDelegatedPromptCell(sourceThreadID string, prompt string) DelegatedPromptCell {
	return DelegatedPromptCell{SourceThreadID: sourceThreadID, Prompt: prompt}
}

// DisplayLines paints the attribution bullet, then the delegated body as
// Markdown indented under it.
func (c DelegatedPromptCell) DisplayLines(width int) []string {
	width = max(width, 1)
	attribution := context.DelegatedToolOutputNotice(c.SourceThreadID, "")
	attribution = strings.TrimRight(attribution, "\n")
	lines := []string{tui.TruncateWithEllipsis("\u2022 "+attribution, width)}
	body := markdownRows(c.Prompt, max(width-2, 1), "", "")
	if len(body) == 0 {
		return lines
	}
	for _, row := range body {
		lines = append(lines, strings.TrimRight("  "+row, " "))
	}
	return lines
}

// RawLines keeps the readable attribution and the original message text.
func (c DelegatedPromptCell) RawLines() []string {
	text := context.DelegatedToolOutputNotice(c.SourceThreadID, c.Prompt)
	return strings.Split(strings.TrimRight(text, "\n"), "\n")
}

var _ HistoryCell = DelegatedPromptCell{}
