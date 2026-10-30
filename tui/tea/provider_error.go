package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/chatwidget/provider_error.rs (#58202).
//
// A provider admission failure keeps the conversation readable as a frozen
// history snapshot, blocks direct input, and replaces the composer with a
// notice offering three recoveries: `c` continues the chat in a new session
// with an unsent continuation prompt, `n` starts fresh, and `Esc` returns to
// the resume picker.

// hasProviderError reports whether the visible chat is blocked by a provider
// admission failure (Rust ChatWidget::has_provider_error).
func (m *Model) hasProviderError() bool {
	if m == nil {
		return false
	}
	return m.providerError != nil
}

// showProviderError enters the provider-error state for the frozen chat,
// preserving any draft and queued messages so a successful retry can restore
// them (Rust ChatWidget::show_provider_error).
func (m *Model) showProviderError(err *appserver.ModelProviderError, rolloutPath string) {
	if m == nil || err == nil {
		return
	}
	m.providerError = err
	m.providerErrorRolloutPath = strings.TrimSpace(rolloutPath)
	m.readOnlyThread = true
	m.threadUnavailable = false
	m.notice = ""
	if m.slashPopup.Active {
		m.slashPopup = slashCommandPopup{}
	}
	m.refreshTranscript()
}

// clearProviderError leaves the provider-error state. The caller owns the
// read-only flag, which a fresh session clears.
func (m *Model) clearProviderError() {
	if m == nil {
		return
	}
	m.providerError = nil
	m.providerErrorRolloutPath = ""
}

// providerErrorCanContinue mirrors Rust's `thread_id.is_some() &&
// current_rollout_path.is_some()` gate on the continuation shortcut.
func (m *Model) providerErrorCanContinue() bool {
	if m == nil || m.State == nil {
		return false
	}
	return strings.TrimSpace(m.State.ThreadID) != "" && strings.TrimSpace(m.providerErrorRolloutPath) != ""
}

// providerErrorTitle mirrors ModelProviderError::title plus the mismatch
// variant's "Continue in a new chat" heading while continuation is possible.
func (m *Model) providerErrorTitle() string {
	if m == nil || m.providerError == nil {
		return ""
	}
	switch m.providerError.Kind {
	case appserver.ModelProviderMismatch:
		if m.providerErrorCanContinue() {
			return "Continue in a new chat"
		}
		return "Model provider mismatch"
	case appserver.ModelProviderNotConfigured:
		return "Model provider unavailable"
	case appserver.ModelProviderPolicyConflict:
		return "Model provider blocked by policy"
	default:
		return "Model provider unavailable"
	}
}

// providerErrorMessage mirrors ChatWidget::provider_error_message: the
// mismatch explanation differs from the protocol error's Display text.
func (m *Model) providerErrorMessage() string {
	if m == nil || m.providerError == nil {
		return ""
	}
	err := m.providerError
	if err.Kind == appserver.ModelProviderMismatch {
		provider := firstNonEmpty(strings.TrimSpace(err.ModelProvider), "the saved provider")
		current := firstNonEmpty(strings.TrimSpace(err.CurrentModelProvider), "another provider")
		return "This chat uses " + provider + ", but Codex now uses " + current + ", so this chat is read-only."
	}
	return err.Error()
}

// handleProviderErrorKey routes the keys the notice advertises. Transcript,
// raw-output, and copy surfaces stay available like the external-writer
// notice; every submission key is ignored.
func (m *Model) handleProviderErrorKey(msg bubbletea.KeyMsg, keySpec string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch msg.Type {
	case bubbletea.KeyCtrlC, bubbletea.KeyCtrlD:
		return bubbletea.Quit
	}
	if m.keyMatches("global", "open_transcript", keySpec) {
		return m.openTranscriptOverlay()
	}
	if m.keyMatches("global", "find_transcript", keySpec) {
		return m.openTranscriptFind()
	}
	if m.keyMatches("global", "toggle_raw_output", keySpec) {
		return m.toggleRawOutputMode()
	}
	if m.keyMatches("global", "copy", keySpec) {
		m.copyLastAgentResponse()
		return nil
	}
	if msg.Type == bubbletea.KeyEsc {
		// Rust #58202: Esc opens the resume picker so the user can pick another
		// chat instead of being trapped on the blocked conversation.
		return m.openSessionPicker(codextui.SessionPickerResume)
	}
	if msg.Type == bubbletea.KeyRunes && len(msg.Runes) == 1 && !msg.Alt {
		switch msg.Runes[0] {
		case 'c':
			if !m.providerErrorCanContinue() {
				return nil
			}
			return m.continueProviderChat()
		case 'n':
			return m.startFreshFromProviderError()
		}
	}
	return nil
}

// continueProviderChat starts a fresh session on the current provider and
// drafts an editable reference to the saved conversation (Rust
// AppEvent::ContinueProviderChat).
func (m *Model) continueProviderChat() bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	path := strings.TrimSpace(m.providerErrorRolloutPath)
	if threadID == "" || path == "" {
		return nil
	}
	name := "our previous conversation"
	if title := strings.TrimSpace(m.State.ThreadName); title != "" {
		name = "\u201c" + title + "\u201d"
	}
	draft := "Read " + name + " and let\u2019s continue where we left off.\n\nSaved conversation: " + path
	m.startFreshNamedSession("", "")
	// The embedded TUI assigns the new thread id on the first submission, so a
	// cleared id also means the fresh-session reset ran. A refusal keeps the
	// old id and leaves its notice in place.
	if strings.TrimSpace(m.State.ThreadID) != threadID {
		m.composer.SetValue(draft)
		m.refreshComposerPrompt()
		m.refreshTranscript()
	}
	return nil
}

// startFreshFromProviderError starts an empty session from the notice's `n`
// shortcut.
func (m *Model) startFreshFromProviderError() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.startFreshNamedSession("", "Started a fresh session.")
	return nil
}

// renderProviderErrorNotice renders the provider-error card in place of the
// composer (Rust ProviderErrorNotice).
func (m *Model) renderProviderErrorNotice() string {
	if m == nil || m.providerError == nil {
		return ""
	}
	width := max(m.width, 20)
	contentWidth := max(width-4, 10)
	title := "\U0001f512  " + m.providerErrorTitle()
	lines := codextui.AdaptiveWrapLine(title, codextui.WrapOptions{
		Width:      contentWidth,
		BreakWords: true,
	})
	if len(lines) == 0 {
		lines = []string{title}
	}
	lines = append(lines, codextui.AdaptiveWrapLine(m.providerErrorMessage(), codextui.WrapOptions{
		Width:            contentWidth,
		InitialIndent:    "    ",
		SubsequentIndent: "    ",
		BreakWords:       true,
	})...)
	card := lipgloss.NewStyle().
		Width(width).
		Padding(0, 2).
		Render(strings.Join(lines, "\n"))
	footer := m.readOnlyDimText(m.providerErrorFooterLine())
	return card + "\n" + footer
}

// providerErrorFooterLine mirrors ProviderErrorNotice's shortcut footer.
func (m *Model) providerErrorFooterLine() string {
	items := make([]string, 0, 3)
	if m.providerErrorCanContinue() {
		items = append(items, "c continue in new chat")
	}
	items = append(items, "n start fresh", "Esc other chats")
	return " " + strings.Join(items, " \u00b7 ")
}
