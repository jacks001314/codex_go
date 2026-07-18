package bottompane

import "codex_go/tui"

// Rust parity: codex-rs/tui/src/bottom_pane/pending_input_preview.rs.

const PreviewLineLimit = 3

type PendingInputPreview struct {
	PendingSteers    []string
	RejectedSteers   []string
	QueuedMessages   []string
	EditBinding      string
	InterruptBinding string
}

func NewPendingInputPreview() *PendingInputPreview {
	return &PendingInputPreview{
		EditBinding:      "Alt+Up",
		InterruptBinding: "Esc",
	}
}

func (p *PendingInputPreview) SetEditBinding(binding string) {
	p.EditBinding = binding
}

func (p *PendingInputPreview) SetInterruptBinding(binding string) {
	p.InterruptBinding = binding
}

func (p *PendingInputPreview) DesiredHeight(width int) int {
	return len(p.RenderLines(width))
}

func (p *PendingInputPreview) RenderLines(width int) []string {
	if p == nil || width < 4 {
		return nil
	}
	if len(p.PendingSteers) == 0 && len(p.RejectedSteers) == 0 && len(p.QueuedMessages) == 0 {
		return nil
	}
	lines := []string{}
	if len(p.PendingSteers) > 0 {
		header := "Messages to be submitted after next tool call"
		if p.InterruptBinding != "" {
			header += " (press " + p.InterruptBinding + " to interrupt and send immediately)"
		}
		pushSectionHeader(&lines, width, header)
		for _, steer := range p.PendingSteers {
			pushPreview(&lines, width, steer)
		}
	}
	if len(p.RejectedSteers) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		pushSectionHeader(&lines, width, "Messages to be submitted at end of turn")
		for _, steer := range p.RejectedSteers {
			pushPreview(&lines, width, steer)
		}
	}
	if len(p.QueuedMessages) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		pushSectionHeader(&lines, width, "Queued follow-up inputs")
		for _, message := range p.QueuedMessages {
			pushPreview(&lines, width, message)
		}
		if p.EditBinding != "" {
			lines = append(lines, "    "+p.EditBinding+" edit last queued message")
		}
	}
	return lines
}

func pushSectionHeader(lines *[]string, width int, header string) {
	prefix := "\u2022 "
	wrapped := tui.AdaptiveWrapLine(prefix+header, tui.WrapOptions{
		Width:            width,
		SubsequentIndent: "  ",
		BreakWords:       true,
	})
	*lines = append(*lines, wrapped...)
}

func pushPreview(lines *[]string, width int, text string) {
	wrapped := []string{}
	for _, rawLine := range rustLines(text) {
		wrapped = append(wrapped, tui.AdaptiveWrapLine(rawLine, tui.WrapOptions{
			Width:            width,
			InitialIndent:    "  \u21ab ",
			SubsequentIndent: "    ",
			BreakWords:       true,
		})...)
	}
	limit := min(len(wrapped), PreviewLineLimit)
	*lines = append(*lines, wrapped[:limit]...)
	if len(wrapped) > PreviewLineLimit {
		*lines = append(*lines, "    \u2026")
	}
}

func rustLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := []string{}
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}
		line := text[start:i]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		lines = append(lines, line)
		start = i + 1
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}
