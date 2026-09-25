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

// commandCenterAvailable mirrors Rust's ExternalWriterNotice
// `command_center_available`: a session connected to a shared app server can
// return to the command center (the embedded TUI marks the dashboard
// unavailable), together with Rust's input gate that refuses an embedded
// app-server target.
func (m *Model) commandCenterAvailable() bool {
	if m == nil {
		return false
	}
	return !m.agentsOverviewEmbedded && m.onAgentsOverviewRefresh != nil
}

// agentsNavigationKeyAvailable mirrors Rust BottomPane::agents_navigation_key_available
// (#48132): `Left` must still move the composer's cursor for the active
// editor/Vim keymap, no global/chat/voice action may claim it, and no chord may
// start with it (Go's keymap has no configurable chord prefixes, so that last
// exclusion has no counterpart here).
func (m *Model) agentsNavigationKeyAvailable() bool {
	if m == nil {
		return false
	}
	// Rust resolves the composer's move_left binding for the active keymap.
	// Go's keymap catalog only exposes Vim normal mode's movement actions; the
	// plain composer's cursor movement is not remappable, so Left stays available
	// there unless another action claims it.
	if m.vimMode && !m.vimInsert {
		bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "vim_normal", "move_left")
		if !keymapBindingsContainKey(bindings, "left") {
			return false
		}
	}
	for _, action := range codextui.KeymapActions(codextui.KeymapActionFilter{}) {
		switch action.Context {
		case "global", "chat", "voice":
		default:
			continue
		}
		resolved, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, action.Context, action.Action)
		if keymapBindingsContainKey(resolved, "left") {
			return false
		}
	}
	return true
}

func keymapBindingsContainKey(bindings []string, keySpec string) bool {
	for _, binding := range bindings {
		if strings.EqualFold(strings.TrimSpace(binding), keySpec) {
			return true
		}
	}
	return false
}

// handleReadOnlyThreadKey routes the keys the read-only notice advertises:
// Left or Esc return to the command center when one is available (Rust #48132),
// R retries, Ctrl+C/Q exit, and the transcript/raw/copy surfaces stay
// available. Everything else is ignored.
func (m *Model) handleReadOnlyThreadKey(msg bubbletea.KeyMsg, keySpec string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.commandCenterAvailable() {
		if msg.Type == bubbletea.KeyEsc || (msg.Type == bubbletea.KeyLeft && m.agentsNavigationKeyAvailable()) {
			return m.applyAgentsCommand()
		}
	} else if msg.Type == bubbletea.KeyEsc {
		return bubbletea.Quit
	}
	switch msg.Type {
	case bubbletea.KeyCtrlC, bubbletea.KeyCtrlD:
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
		case 'f', 'F':
			// Rust's locked-thread fork shortcut: f/F (plain or shifted) opens an
			// editable copy of the thread without taking the source lease. Ctrl
			// and Super combinations arrive as their own key types, so a rune
			// without Alt is the unmodified (or shifted) press Rust accepts, and a
			// blocking view keeps ownership of the key.
			if !msg.Alt && m.modal == nil {
				return m.applyForkCurrentSession("")
			}
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
	footer := m.readOnlyDimText(m.readOnlyFooterLine())
	return card + "\n" + footer
}

// readOnlyFooterLine mirrors Rust ExternalWriterNotice::footer_lines: the retry
// key, the command-center key when one is available (Left/Esc, or Esc once Left
// was remapped), the exit keys, and the transcript shortcut.
func (m *Model) readOnlyFooterLine() string {
	items := []string{"r retry", "f fork"}
	if m.commandCenterAvailable() {
		key := "Esc"
		if m.agentsNavigationKeyAvailable() {
			key = "\u2190/Esc"
		}
		items = append(items, key+" command center", "ctrl+c/q exit")
	} else {
		items = append(items, "Esc/ctrl+c/q exit")
	}
	items = append(items, "Ctrl+T transcript")
	return " " + strings.Join(items, "   ")
}

func (m *Model) readOnlyDimText(text string) string {
	return m.styleAsyncQuestionText(text, m.styles().Chat.DimText)
}

func (m *Model) readOnlyAccentText(text string) string {
	return m.styleAsyncQuestionText(text, m.styles().Dialog.Highlight+m.styles().ExecCell.Bold)
}
