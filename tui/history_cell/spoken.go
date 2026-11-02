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

// SpokenHistoryCell renders one completed voice caption. User prompts use
// NewSpokenUserPrompt instead (Rust new_spoken_user_prompt), so this cell is
// the assistant transcript. When Workspace is set, file references in the
// caption are linked inside that workspace (Rust spoken_artifacts).
type SpokenHistoryCell struct {
	Role string
	Text string
	// Workspace is the local session workspace used to validate artifact
	// links. Empty disables linking (no local workspace, or an SSH session).
	Workspace string
}

// SpokenPromptMarkerCell is implemented by cells that render a spoken user
// prompt, whose red chevron the split-flap board must keep coloured.
type SpokenPromptMarkerCell interface {
	SpokenPromptMarker() bool
}

// NewSpokenHistoryCell returns a cell for a completed voice caption. An empty
// role renders as the assistant, matching the primary speaker.
func NewSpokenHistoryCell(role string, text string) SpokenHistoryCell {
	return SpokenHistoryCell{Role: strings.TrimSpace(role), Text: text}
}

// NewSpokenAssistantCell returns the assistant caption cell for a voice
// conversation whose workspace can trust local artifact links.
func NewSpokenAssistantCell(text string, workspace string) SpokenHistoryCell {
	return SpokenHistoryCell{Role: "assistant", Text: text, Workspace: strings.TrimSpace(workspace)}
}

// DisplayLines renders the caption with its speaker label, linking workspace
// artifacts in assistant captions.
func (c SpokenHistoryCell) DisplayLines(width int) []string {
	_ = width
	lines := c.lines()
	if c.Workspace == "" || strings.EqualFold(c.Role, "user") {
		return lines
	}
	return AnnotateSpokenArtifacts(lines, c.Workspace)
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
