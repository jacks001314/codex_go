package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/chatwidget/rendering.rs ExternalWriterNotice
// (#43253). A conversation owned by another app opens as a read-only history
// snapshot: the composer is replaced by a notice, R retries the resume, and
// every other input is ignored until the other app closes it.

// setReadOnlyThread enters or leaves the read-only external-writer state.
func (m *Model) setReadOnlyThread(readOnly bool) {
	if m == nil {
		return
	}
	m.readOnlyThread = readOnly
	if !readOnly {
		return
	}
	// Block input while preserving the transcript; drafts and queued messages
	// survive for a successful retry.
	m.composer.Reset()
	m.attachments = nil
	m.composerMentionBindings = nil
	m.composerPasteEnterUntil = nil
	m.notice = ""
}

// handleReadOnlyThreadKey routes the keys the read-only notice advertises:
// R retries, Esc/Ctrl+C/Q exit, and the transcript/raw/copy surfaces stay
// available. Everything else is ignored.
func (m *Model) handleReadOnlyThreadKey(msg bubbletea.KeyMsg, keySpec string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch msg.Type {
	case bubbletea.KeyCtrlC, bubbletea.KeyCtrlD, bubbletea.KeyEsc:
		return bubbletea.Quit
	}
	if m.keyMatches("global", "open_transcript", keySpec) {
		return m.openTranscriptOverlay()
	}
	if m.keyMatches("global", "toggle_raw_output", keySpec) {
		return m.toggleRawOutputMode()
	}
	if m.keyMatches("global", "copy", keySpec) {
		m.copyLastAgentResponse()
		return nil
	}
	if msg.Type == bubbletea.KeyRunes && len(msg.Runes) == 1 {
		switch msg.Runes[0] {
		case 'r', 'R':
			return m.retryReadOnlyThread()
		case 'q', 'Q':
			return bubbletea.Quit
		}
	}
	return nil
}

// retryReadOnlyThread re-runs the resume for the read-only conversation; a
// successful retry clears the read-only state through applyResumeResponse.
func (m *Model) retryReadOnlyThread() bubbletea.Cmd {
	if m == nil || m.State == nil || m.onResumeSession == nil {
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		return nil
	}
	response, err := m.onResumeSession(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{ThreadID: threadID},
	})
	if err != nil {
		m.notice = "Retry failed: " + strings.TrimSpace(err.Error())
		m.refreshTranscript()
		return nil
	}
	if response.Summary != nil {
		m.upsertSessionItem(*response.Summary)
	}
	m.applyResumeResponse(threadID, response)
	return nil
}

// renderReadOnlyThreadNotice renders the external-writer card in place of the
// composer (Rust ExternalWriterNotice).
func (m *Model) renderReadOnlyThreadNotice() string {
	if m == nil {
		return ""
	}
	width := max(m.width, 20)
	contentWidth := max(width-4, 10)
	title := "\U0001f512  This conversation is open in another app"
	retry := "R to Retry"
	lines := codextui.AdaptiveWrapLine(title, codextui.WrapOptions{
		Width:      contentWidth,
		BreakWords: true,
	})
	if len(lines) == 0 {
		lines = []string{title}
	}
	if len(lines) == 1 && codextui.DisplayWidth(lines[0])+codextui.DisplayWidth(retry)+2 <= contentWidth {
		gap := contentWidth - codextui.DisplayWidth(lines[0]) - codextui.DisplayWidth(retry)
		lines[0] = lines[0] + strings.Repeat(" ", gap) + m.readOnlyAccentText(retry)
	} else {
		lines = append(lines, m.readOnlyAccentText(retry))
	}
	lines = append(lines, codextui.AdaptiveWrapLine(
		"Close it there and press R to continue here.",
		codextui.WrapOptions{
			Width:            contentWidth,
			InitialIndent:    "    ",
			SubsequentIndent: "    ",
			BreakWords:       true,
		},
	)...)
	card := lipgloss.NewStyle().
		Width(width).
		Padding(0, 2).
		Render(strings.Join(lines, "\n"))
	footer := m.readOnlyDimText(" r retry   Esc/Ctrl+C/q exit   Ctrl+T transcript")
	return card + "\n" + footer
}

func (m *Model) readOnlyDimText(text string) string {
	return m.styleAsyncQuestionText(text, m.styles().Chat.DimText)
}

func (m *Model) readOnlyAccentText(text string) string {
	return m.styleAsyncQuestionText(text, m.styles().Dialog.Highlight+m.styles().ExecCell.Bold)
}
