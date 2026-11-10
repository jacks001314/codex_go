package tea

import (
	"strings"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

// Rust parity: codex-rs/tui/src/transcript_view/prompt_header.rs (#46739).
//
// The transcript view keeps the prompt of the first visible answer in a
// presentation-only header row: while a long answer scrolls, the reader still
// sees which question it belongs to. The row is chrome, so content, copy, find
// and export keep reading the transcript lines only.

const (
	// pinnedPromptHeaderMinWidth is Rust's prompt_header::line width gate.
	pinnedPromptHeaderMinWidth = 16
	// pinnedPromptHeaderMinBody is the content height below which the header
	// yields so the view keeps four readable rows (Rust gates on
	// area.height >= 4 before reserving the header).
	pinnedPromptHeaderMinBody = 4
)

// syncOverlayPinnedHeader keeps the overlay's chrome row in step with its
// scroll position and presentation.
func (m *Model) syncOverlayPinnedHeader() {
	if m == nil || m.overlay == nil {
		return
	}
	m.overlay.SetPinnedHeader(m.overlayPinnedPromptHeader())
}

// overlayPinnedPromptHeader renders the prompt that owns the first visible
// row, or "" when the row must yield: a visible prompt supplies its own
// context, a narrow or short view has no room for chrome, and a transcript
// without a preceding prompt has nothing to pin.
func (m *Model) overlayPinnedPromptHeader() string {
	if m == nil || m.overlay == nil || m.State == nil || !m.overlayTranscript {
		return ""
	}
	if m.width < pinnedPromptHeaderMinWidth || m.height-3 < pinnedPromptHeaderMinBody {
		return ""
	}
	messages := m.State.Messages
	if len(messages) == 0 {
		return ""
	}
	expanded := true
	if m.backtrack.OverlayPreviewActive {
		expanded = m.browsingDetails
	}
	view, cache := m.overlayTranscriptView()
	_, ranges := renderTranscriptMessagesWithRanges(cache, view, m.rawOutput, m.width, m.activeTUITheme(), expanded, m.sessionCWD, m.showRawReasoning)
	first, ok := transcriptMessageAtLine(ranges, m.overlay.YOffset())
	if !ok || first >= len(messages) {
		return ""
	}
	// Rust prompt_header::line: only pin a preceding prompt when the first
	// visible cell is not itself a prompt with content.
	if _, visible := visibleUserPromptText(messages[first]); visible {
		return ""
	}
	for index := first; index >= 0; index-- {
		text, visible := visibleUserPromptText(messages[index])
		if !visible {
			continue
		}
		spoken := strings.HasPrefix(messages[index].Text, historycell.SpokenPromptMarkerSGR+"\u203a")
		return historycell.PinnedPromptHeaderLine(text, spoken, m.width)
	}
	return ""
}

// visibleUserPromptText reports whether message is a user prompt that renders
// content and returns the text the header shows. Rust
// UserHistoryCell::has_visible_content accepts text or attachments.
func visibleUserPromptText(message codextui.Message) (string, bool) {
	if message.Role != codextui.RoleUser {
		return "", false
	}
	text := message.UserPrompt
	if strings.TrimSpace(text) == "" {
		text = message.Text
	}
	if strings.TrimSpace(text) == "" && len(message.UserPromptLocalImages) == 0 && len(message.UserPromptRemoteImages) == 0 {
		return "", false
	}
	return text, true
}

// transcriptMessageAtLine maps a content row to the message that owns it. A
// separator row belongs to the message it introduces, which is the first
// visible entry Rust pins from.
func transcriptMessageAtLine(ranges [][2]int, line int) (int, bool) {
	if line < 0 {
		line = 0
	}
	for index, span := range ranges {
		if span[1] <= span[0] {
			continue
		}
		if line < span[1] {
			return index, true
		}
	}
	return 0, false
}
