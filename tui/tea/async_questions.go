package tea

import (
	"strconv"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	codextui "codex_go/tui"
	"codex_go/tui/bottom_pane"
	"codex_go/tui/styles"
)

// Async question editing mirrors Rust's chatwidget/bottom_pane question editor
// (#42889/#42891/#42903): questions arrive on async agent messages, the focused
// question is answered through the composer, and only an accepted local
// submission or an explicit skip removes the question.

// asyncQuestionEditorActive reports whether the composer is currently editing a
// pending async question.
func (m *Model) asyncQuestionEditorActive() bool {
	return m != nil && m.asyncQuestions.UnansweredCount() > 0
}

// applyAsyncQuestionKey routes the question navigation, skip, and escape keys.
// It returns false so unrelated keys keep flowing to the composer, which edits
// the focused question's draft while the editor is expanded.
func (m *Model) applyAsyncQuestionKey(msg bubbletea.KeyMsg, keySpec string) bool {
	if !m.asyncQuestionEditorActive() {
		return false
	}
	if m.asyncQuestions.Expanded() {
		switch {
		case m.keyMatches("chat", "skip_question", keySpec):
			m.acceptAsyncQuestion()
			return true
		case m.keyMatches("chat", "edit_queued_message", keySpec):
			if draft, moved := m.asyncQuestions.Navigate(true, m.composer.Value()); moved {
				m.composer.SetValue(draft)
				m.resetVimEditHistory()
			}
			return true
		case m.keyMatches("chat", "prompt_stack_back", keySpec):
			if draft, moved := m.asyncQuestions.Navigate(false, m.composer.Value()); moved {
				m.composer.SetValue(draft)
				m.resetVimEditHistory()
			} else {
				m.collapseAsyncQuestions()
			}
			return true
		case m.questionEscBack && msg.Type == bubbletea.KeyEsc:
			m.collapseAsyncQuestions()
			return true
		}
		return false
	}
	// Collapsed: the edit binding focuses the first unanswered question, unless
	// a popup owns the key (Rust no_modal_or_popup_active).
	if m.slashPopup.Active || m.skillPopup.Active || m.modal != nil || m.overlay != nil {
		return false
	}
	if m.keyMatches("chat", "edit_queued_message", keySpec) {
		m.expandAsyncQuestions()
		return true
	}
	return false
}

// expandAsyncQuestions gives the composer over to the focused question draft,
// stashing the main composer draft so collapsing restores it.
func (m *Model) expandAsyncQuestions() {
	if m == nil || m.asyncQuestions.UnansweredCount() == 0 || m.asyncQuestions.Expanded() {
		return
	}
	m.asyncQuestionMainDraft = m.composer.Value()
	m.asyncQuestions.SetExpanded(true, "")
	m.composer.SetValue(m.asyncQuestions.CurrentDraft())
	m.resetVimEditHistory()
	m.clearComposerPasteWindow()
}

// collapseAsyncQuestions keeps the focused draft and restores the main
// composer draft.
func (m *Model) collapseAsyncQuestions() {
	if m == nil || !m.asyncQuestions.Expanded() {
		return
	}
	m.asyncQuestions.SetExpanded(false, m.composer.Value())
	m.composer.SetValue(m.asyncQuestionMainDraft)
	m.asyncQuestionMainDraft = ""
	m.resetVimEditHistory()
}

// acceptAsyncQuestion removes the focused question (submitted or skipped) and
// moves the composer to the next draft, or back to the main draft when none
// remain.
func (m *Model) acceptAsyncQuestion() {
	if m == nil || m.asyncQuestions.UnansweredCount() == 0 {
		return
	}
	draft := m.asyncQuestions.AcceptAnswer()
	if m.asyncQuestions.UnansweredCount() == 0 {
		m.asyncQuestions.SetExpanded(false, draft)
		m.composer.SetValue(m.asyncQuestionMainDraft)
		m.asyncQuestionMainDraft = ""
	} else {
		m.composer.SetValue(draft)
	}
	m.resetVimEditHistory()
}

// submitAsyncQuestionAnswer frames the composer draft as the current question's
// answer and delivers it. While a turn is running the answer is queued, matching
// Rust's queue_user_message path for asynchronous answers.
func (m *Model) submitAsyncQuestionAnswer(queue bool) bubbletea.Cmd {
	if m == nil || m.asyncQuestions.UnansweredCount() == 0 {
		return nil
	}
	if m.misalignmentPolicyStopped {
		// Rust only consumes an answer when the local send is accepted; a
		// stopped chat keeps the question pending.
		m.notice = "Chat stopped as a precaution. Start or resume another chat to continue."
		return nil
	}
	question, ok := m.asyncQuestions.CurrentQuestion()
	if !ok {
		return nil
	}
	answer, limit, ready, tooLong := bottompane.BuildAsyncQuestionAnswer(question, strings.TrimSpace(m.composer.Value()))
	if tooLong {
		m.notice = "Answer too long; limit " + strconv.Itoa(limit) + " characters"
		return nil
	}
	if !ready {
		return nil
	}
	// The answer is plain text: attachments and mention bindings belong to the
	// stashed main draft.
	attachments := m.attachments
	mentionBindings := m.composerMentionBindings
	m.attachments = nil
	m.composerMentionBindings = nil
	m.composer.SetValue(answer)
	m.asyncQuestionAnswerInFlight = true
	var cmd bubbletea.Cmd
	if queue {
		cmd = m.queueComposer(false)
	} else {
		cmd = m.submitComposer()
	}
	m.asyncQuestionAnswerInFlight = false
	m.attachments = attachments
	m.composerMentionBindings = mentionBindings
	m.acceptAsyncQuestion()
	m.refreshTranscript()
	return cmd
}

// clearAsyncQuestionsForNewPrompt mirrors Rust #44328: submitting or queueing a
// new prompt drops the previous prompt's unanswered questions while retaining
// their seen IDs, so replay cannot restore them. Answers and local commands
// preserve pending questions.
func (m *Model) clearAsyncQuestionsForNewPrompt() {
	if m == nil || m.asyncQuestionAnswerInFlight {
		return
	}
	if m.asyncQuestions.UnansweredCount() == 0 {
		return
	}
	m.asyncQuestions.ClearPending()
	m.asyncQuestionMainDraft = ""
}

// renderAsyncQuestions renders the collapsed summary or the expanded question
// above the composer.
func (m *Model) renderAsyncQuestions() []string {
	if !m.asyncQuestionEditorActive() {
		return nil
	}
	count := m.asyncQuestions.UnansweredCount()
	if !m.asyncQuestions.Expanded() {
		line := "  " + m.dimAsyncQuestionText("?") + " " +
			m.accentAsyncQuestionText(questionCountLabel(count))
		if binding := m.asyncQuestionEditBindingLabel(); binding != "" {
			line += m.dimAsyncQuestionText(" · " + binding + " to answer")
		}
		return []string{line}
	}
	lines := []string{}
	if count > 1 {
		lines = append(lines, m.dimAsyncQuestionText(m.asyncQuestions.ProgressPrefixText()))
	}
	question, ok := m.asyncQuestions.CurrentQuestion()
	if !ok {
		return lines
	}
	width := max(m.width-2, 8)
	wrapped := lipgloss.NewStyle().Width(width).Render(question.Title)
	for _, line := range strings.Split(wrapped, "\n") {
		lines = append(lines, m.accentAsyncQuestionText(strings.TrimRight(line, " ")))
	}
	return lines
}

func questionCountLabel(count int) string {
	if count == 1 {
		return "1 question"
	}
	return strconv.Itoa(count) + " questions"
}

// asyncQuestionEditBindingLabel resolves the configured edit binding for the
// "N questions · <binding> to answer" hint.
func (m *Model) asyncQuestionEditBindingLabel() string {
	if m == nil || m.keymapConfig == nil {
		return "Alt+Up"
	}
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "chat", "edit_queued_message")
	if len(bindings) == 0 {
		return ""
	}
	return displayKeyBinding(bindings[0])
}

func displayKeyBinding(binding string) string {
	parts := strings.Split(strings.TrimSpace(binding), "-")
	if len(parts) == 0 {
		return binding
	}
	out := make([]string, 0, len(parts))
	for index, part := range parts {
		switch strings.ToLower(part) {
		case "":
			continue
		case "ctrl", "control":
			out = append(out, "Ctrl")
		case "alt", "option":
			out = append(out, "Alt")
		case "shift":
			out = append(out, "Shift")
		default:
			if index == len(parts)-1 {
				out = append(out, keyDisplayName(part))
			} else {
				out = append(out, keyDisplayName(part))
			}
		}
	}
	return strings.Join(out, "+")
}

func keyDisplayName(key string) string {
	switch strings.ToLower(key) {
	case "up":
		return "Up"
	case "down":
		return "Down"
	case "left":
		return "Left"
	case "right":
		return "Right"
	case "esc":
		return "Esc"
	case "enter":
		return "Enter"
	case "tab":
		return "Tab"
	case "space":
		return "Space"
	case "backspace":
		return "Backspace"
	}
	if len(key) == 1 {
		return strings.ToUpper(key)
	}
	return key
}

func (m *Model) dimAsyncQuestionText(text string) string {
	return m.styleAsyncQuestionText(text, m.styles().Chat.DimText)
}

func (m *Model) accentAsyncQuestionText(text string) string {
	return m.styleAsyncQuestionText(text, m.styles().Dialog.Highlight+m.styles().ExecCell.Bold)
}

func (m *Model) styleAsyncQuestionText(text string, style string) string {
	if styles := m.styles(); styles.ExecCell.Reset != "" && style != "" {
		return style + text + styles.ExecCell.Reset
	}
	return text
}

// styles returns the model's theme styles, falling back to the dark defaults.
func (m *Model) styles() styles.Styles {
	if m == nil {
		return styles.DefaultDark()
	}
	return m.Styles
}
