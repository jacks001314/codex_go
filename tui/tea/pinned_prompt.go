package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

// Rust parity: transcript_view/prompt_header.rs. A prompt that has scrolled out
// of view is pinned above the transcript; clicking the pinned row jumps back to
// the original message.

type pinnedPromptState struct {
	active     bool
	row        int
	promptLine int
}

func (m *Model) pinnedPromptHeader() (string, int, bool) {
	if m == nil || m.State == nil || m.rawOutput || m.transcript.Width < 16 {
		return "", -1, false
	}
	if m.modal != nil || m.overlay != nil || m.mainFind.active() || m.activityFocus.active ||
		m.readOnlyThread || m.editorActive || m.windowsSandboxSetupActive || m.vimSearchMode {
		return "", -1, false
	}
	_, ranges := renderTranscriptMessagesWithRanges(
		&m.transcriptMessages,
		m.State,
		false,
		m.transcript.Width,
		m.activeTUITheme(),
		false,
		m.sessionCWD,
		m.showRawReasoning,
	)
	if len(ranges) != len(m.State.Messages) {
		return "", -1, false
	}
	firstVisible := -1
	for index, span := range ranges {
		if span[1] <= span[0] {
			continue
		}
		// The viewport offset may point at a blank separator between entries;
		// the first entry ending after it is the first visible content.
		if span[1] > m.transcript.YOffset {
			firstVisible = index
			break
		}
	}
	if firstVisible < 0 {
		return "", -1, false
	}
	if m.State.Messages[firstVisible].Role == codextui.RoleUser {
		return "", -1, false
	}
	promptIndex := -1
	for index := firstVisible - 1; index >= 0; index-- {
		if m.State.Messages[index].Role != codextui.RoleUser {
			continue
		}
		if strings.TrimSpace(m.State.Messages[index].UserPrompt+" "+m.State.Messages[index].Text) != "" {
			promptIndex = index
			break
		}
	}
	if promptIndex < 0 {
		return "", -1, false
	}
	message := m.State.Messages[promptIndex]
	text := strings.TrimSpace(message.UserPrompt)
	if text == "" {
		text = strings.TrimSpace(message.Text)
	}
	text = historycell.SanitizeUserText(text)
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "", -1, false
	}
	if len([]rune(text)) > 512 {
		text = string([]rune(text)[:512]) + "\u2026"
	}
	header := codextui.TruncateWithEllipsis("\u203a "+text, m.transcript.Width)
	return header, ranges[promptIndex][0], true
}

// applyPinnedPromptHeader replaces the first visible transcript row with the
// pinned prompt. The transcript viewport keeps its height, so the composer
// stays fixed.
func (m *Model) applyPinnedPromptHeader(transcript string, statusHeight int) string {
	header, promptLine, ok := m.pinnedPromptHeader()
	if !ok {
		m.pinnedPrompt = pinnedPromptState{}
		return transcript
	}
	lines := strings.Split(transcript, "\n")
	if len(lines) == 0 {
		m.pinnedPrompt = pinnedPromptState{}
		return transcript
	}
	lines[0] = header
	m.pinnedPrompt = pinnedPromptState{active: true, row: statusHeight, promptLine: promptLine}
	return strings.Join(lines, "\n")
}

// handlePinnedPromptClick jumps to the original prompt when the pinned header
// is clicked. It clears competing transcript interactions first.
func (m *Model) handlePinnedPromptClick(msg bubbletea.MouseMsg) bool {
	if m == nil || !m.pinnedPrompt.active {
		return false
	}
	if msg.Action != bubbletea.MouseActionPress || msg.Button != bubbletea.MouseButtonLeft ||
		msg.Y != m.pinnedPrompt.row {
		return false
	}
	if m.mainFind.active() {
		m.cancelMainTranscriptFind()
	}
	m.activityFocus.active = false
	m.renderedSelection.clear()
	m.scrollTranscriptToLine(m.pinnedPrompt.promptLine)
	m.pinnedPrompt = pinnedPromptState{}
	m.refreshTranscript()
	return true
}
