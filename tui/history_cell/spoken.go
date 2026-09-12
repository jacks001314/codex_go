package historycell

// Spoken transcript history. Rust renders voice captions through
// new_spoken_user_prompt and the spoken agent-markdown cell so a conversation
// that was spoken is visually distinct from typed input.

import (
	"strings"
)

// Voice caption markers. Spoken turns are labelled so a copied transcript still
// distinguishes speech from typing.
const (
	SpokenUserPrefix      = "🎤 User (voice): "
	SpokenAssistantPrefix = "🔊 Assistant (voice): "
	SpokenCaptionPrefix   = "   "
)

// SpokenHistoryCell renders one completed voice caption.
type SpokenHistoryCell struct {
	Role string
	Text string
}

// NewSpokenHistoryCell returns a cell for a completed voice caption. An empty
// role renders as the assistant, matching the primary speaker.
func NewSpokenHistoryCell(role string, text string) SpokenHistoryCell {
	return SpokenHistoryCell{Role: strings.TrimSpace(role), Text: text}
}

// DisplayLines renders the caption with its speaker label.
func (c SpokenHistoryCell) DisplayLines(width int) []string {
	_ = width
	return c.lines()
}

// RawLines renders the caption for copy-friendly scrollback.
func (c SpokenHistoryCell) RawLines() []string {
	return c.lines()
}

func (c SpokenHistoryCell) lines() []string {
	prefix := SpokenAssistantPrefix
	if strings.EqualFold(c.Role, "user") {
		prefix = SpokenUserPrefix
	}
	text := strings.TrimSpace(c.Text)
	if text == "" {
		return []string{prefix}
	}
	parts := strings.Split(text, "\n")
	lines := make([]string, 0, len(parts))
	for index, part := range parts {
		if index == 0 {
			lines = append(lines, prefix+part)
			continue
		}
		lines = append(lines, SpokenCaptionPrefix+part)
	}
	return lines
}

var _ HistoryCell = SpokenHistoryCell{}
