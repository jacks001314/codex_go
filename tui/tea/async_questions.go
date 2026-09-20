package tea

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/tui/bottom_pane"
	"codex_go/tui/chatwidget"
	"codex_go/tui/styles"
)

// Async question editing mirrors Rust's chatwidget/bottom_pane question editor
// (#42889/#42891/#42894/#42897/#42903): questions arrive on async agent
// messages, suggested answers are selectable (with an appended editable Other
// choice), and only an accepted local submission or an explicit skip removes
// the question.

// asyncQuestionEditorActive reports whether the model holds pending questions.
func (m *Model) asyncQuestionEditorActive() bool {
	return m != nil && m.asyncQuestions.UnansweredCount() > 0
}

// asyncQuestionCountdownMsg refreshes the collapsed question countdown. Bubble
// Tea has no frame delay, so the model schedules its own one-second tick while
// a countdown is active (Rust #42903 next_frame_delay).
type asyncQuestionCountdownMsg struct{}

// asyncQuestionCountdownCmd schedules the next countdown refresh, or returns nil
// once no question has an active countdown.
func (m *Model) asyncQuestionCountdownCmd() bubbletea.Cmd {
	if m == nil || m.asyncQuestions.TimerRemaining(m.currentTime()) <= 0 {
		return nil
	}
	return bubbletea.Tick(time.Second, func(time.Time) bubbletea.Msg {
		return asyncQuestionCountdownMsg{}
	})
}

// asyncQuestionNotificationTitle mirrors Rust's #46574 title rule: a single
// titled question shows the truncated title, a single untitled arrival reads
// "Question requested", and a batch reports how many questions arrived.
func asyncQuestionNotificationTitle(questions []bottompane.AsyncUserInputQuestion, addedCount int) string {
	if len(questions) == 1 {
		if title := strings.TrimSpace(questions[0].Title); title != "" {
			return chatwidget.TruncateRunes(title, 30)
		}
	}
	if addedCount == 1 {
		return "Question requested"
	}
	return strconv.Itoa(addedCount) + " questions requested"
}

// applyAsyncQuestionKey routes question navigation, skip, escape, and choice
// selection. It returns handled=false so unrelated keys keep flowing to the
// composer, which edits the focused question's draft while the editor is
// expanded.
func (m *Model) applyAsyncQuestionKey(msg bubbletea.KeyMsg, keySpec string) (bubbletea.Cmd, bool) {
	if !m.asyncQuestionEditorActive() {
		return nil, false
	}
	if !m.asyncQuestions.Expanded() {
		// Collapsed: the edit binding focuses the first unanswered question,
		// unless a popup owns the key (Rust no_modal_or_popup_active).
		if m.slashPopup.Active || m.skillPopup.Active || m.modal != nil || m.overlay != nil {
			return nil, false
		}
		if m.keyMatches("chat", "edit_queued_message", keySpec) {
			m.expandAsyncQuestions()
			return nil, true
		}
		return nil, false
	}
	// Opening or using the editor stops the collapsed countdown (Rust #42903).
	m.asyncQuestions.SnoozeAutoResolution()
	switch {
	case m.keyMatches("chat", "skip_question", keySpec):
		m.acceptAsyncQuestion()
		return nil, true
	case m.keyMatches("chat", "edit_queued_message", keySpec):
		return nil, m.advanceAsyncQuestionsOrRestoreQueued()
	case m.keyMatches("chat", "prompt_stack_back", keySpec):
		m.navigateAsyncQuestions(false)
		return nil, true
	case m.questionEscBack && msg.Type == bubbletea.KeyEsc:
		m.collapseAsyncQuestions()
		return nil, true
	}
	if !m.asyncQuestions.HasOptions() {
		// Free-text question: the composer owns the draft.
		return nil, false
	}
	if m.asyncQuestions.OtherSelected() {
		// The editable Other choice has notes focus; Up/Down still move between
		// choices (Rust keeps arrows out of the inline editor).
		switch msg.Type {
		case bubbletea.KeyUp:
			m.asyncQuestions.MoveSelection(false)
			return nil, true
		case bubbletea.KeyDown:
			m.asyncQuestions.MoveSelection(true)
			return nil, true
		}
		return nil, false
	}
	// A suggested option is focused: list navigation moves the selection,
	// printable defaults (k/j) yield to typing into Other, and Enter submits.
	if action, remapped := m.asyncQuestionListAction(keySpec); action != "" {
		if isPlainPrintableKeySpec(keySpec) && !remapped {
			// Printable list defaults (k/j) yield to typing, which opens Other.
			m.asyncQuestions.SelectOther()
			return nil, false
		}
		switch action {
		case "move_up":
			m.asyncQuestions.MoveSelection(false)
			return nil, true
		case "move_down":
			m.asyncQuestions.MoveSelection(true)
			return nil, true
		case "page_up":
			m.asyncQuestions.PageSelection(false)
			return nil, true
		case "page_down":
			m.asyncQuestions.PageSelection(true)
			return nil, true
		case "jump_top":
			m.asyncQuestions.JumpSelection(true)
			return nil, true
		case "jump_bottom":
			m.asyncQuestions.JumpSelection(false)
			return nil, true
		case "cancel":
			m.collapseAsyncQuestions()
			return nil, true
		case "accept":
			// Let the composer submit branch answer the focused choice.
			return nil, false
		default:
			// move_left/move_right have no meaning in the choice list.
			return nil, true
		}
	}
	if msg.Type == bubbletea.KeyRunes && len(msg.Runes) == 1 && !msg.Alt {
		if index, ok := asyncQuestionDigitIndex(msg.Runes[0], m.asyncQuestions.ChoiceCount()); ok {
			m.asyncQuestions.SelectOption(index)
			if m.asyncQuestions.OtherSelected() {
				// Focusing Other must not submit; the digit opens the editor.
				return nil, true
			}
			return m.submitAsyncQuestionAnswer(false), true
		}
		// Any other printable input opens the editable Other choice.
		m.asyncQuestions.SelectOther()
		return nil, false
	}
	// Unhandled keys stay inside the question view (Rust consumes them).
	return nil, true
}

// asyncQuestionListAction resolves the list action bound to a key spec and
// whether the binding was remapped from Rust's default (Rust #42897 only lets
// remapped printable list bindings keep their list meaning).
func (m *Model) asyncQuestionListAction(keySpec string) (string, bool) {
	if keySpec == "" {
		return "", false
	}
	for _, action := range []string{
		"move_up", "move_down", "page_up", "page_down", "jump_top", "jump_bottom", "accept", "cancel", "move_left", "move_right",
	} {
		if !m.keyMatches("list", action, keySpec) {
			continue
		}
		_, _, custom := codextui.ResolvedKeymapBindings(m.keymapConfig, "list", action)
		return action, custom
	}
	return "", false
}

// isPlainPrintableKeySpec reports whether a normalized key spec is a bare
// printable character (for example "k" or "1", but not "ctrl-k" or "up").
func isPlainPrintableKeySpec(keySpec string) bool {
	if keySpec == "" || strings.Contains(keySpec, "-") {
		return false
	}
	if utf8.RuneCountInString(keySpec) != 1 {
		return false
	}
	r, _ := utf8.DecodeRuneInString(keySpec)
	return r >= 0x21 && r <= 0x7e
}

// asyncQuestionDigitIndex mirrors Rust's option_index_for_digit: digits 1..N map
// to choices, with 0 and out-of-range digits rejected.
func asyncQuestionDigitIndex(r rune, choiceCount int) (int, bool) {
	if r < '1' || r > '9' {
		return 0, false
	}
	index := int(r - '1')
	if index >= choiceCount {
		return 0, false
	}
	return index, true
}

// navigateAsyncQuestions moves between questions, restoring each question's
// draft; backward navigation at the first question collapses the editor.
func (m *Model) navigateAsyncQuestions(forward bool) {
	draft, moved := m.asyncQuestions.Navigate(forward, m.composer.Value())
	if moved {
		m.composer.SetValue(draft)
		m.resetVimEditHistory()
		return
	}
	if !forward {
		m.collapseAsyncQuestions()
	}
}

// resolveAsyncQuestionsFromReplyText drops the pending questions a committed
// desktop reply answers, mirroring Rust's AsyncQuestions::resolve_answers
// (#46486). It reports whether any question was resolved.
func (m *Model) resolveAsyncQuestionsFromReplyText(text string) bool {
	if m == nil {
		return false
	}
	replies := codextui.ParseAsyncQuestionReplies(text)
	if len(replies) == 0 {
		return false
	}
	ids := make([]string, 0, len(replies))
	for _, reply := range replies {
		if id := strings.TrimSpace(reply.QuestionItemID); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return false
	}
	resolved, currentAnswered := m.asyncQuestions.ResolveAsyncQuestionAnswers(ids)
	if !resolved {
		return false
	}
	// Only an answered focused question re-syncs the composer (Rust flushes the
	// pending input there), so an unstored draft for another question survives.
	if currentAnswered && m.asyncQuestions.UnansweredCount() > 0 {
		m.composer.SetValue(m.asyncQuestions.CurrentDraft())
	}
	return true
}

// advanceAsyncQuestionsOrRestoreQueued mirrors Rust #42903: forward navigation
// from the last question collapses the editor and hands the key to the
// queued-message edit so the latest queued message becomes the main draft. It
// reports whether the key was consumed.
func (m *Model) advanceAsyncQuestionsOrRestoreQueued() bool {
	draft, moved := m.asyncQuestions.Navigate(true, m.composer.Value())
	if moved {
		m.composer.SetValue(draft)
		m.resetVimEditHistory()
		return true
	}
	if len(m.queued) > 0 && m.modal == nil && !m.slashPopup.Active && !m.skillPopup.Active {
		m.collapseAsyncQuestions()
		// Let applyEditQueuedMessageKey restore the latest queued message.
		return false
	}
	return true
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

// submitAsyncQuestionAnswer frames the focused answer and delivers it. A
// suggested option must be fully visible before it can authorize the answer;
// the editable Other draft must not be blank. While a turn is running the
// answer is queued, matching Rust's queue_user_message path.
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
	questionID := m.asyncQuestions.CurrentQuestionID()
	text, ready := m.asyncQuestions.AnswerText(m.composer.Value())
	if !ready {
		return nil
	}
	if m.asyncQuestions.AnswerIsNamedChoice() && !m.asyncQuestionChoicesVisible() {
		m.notice = "Expand terminal to read the entire option"
		return nil
	}
	answer, ready, tooLong := bottompane.BuildAsyncQuestionAnswer(questionID, question, text)
	if tooLong {
		m.notice = "Answer too long; shorten it before sending"
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

// appendBufferedAsyncQuestions retains questions carried by buffered live
// notifications when switching to a background thread (Rust #42903). Historical
// turn replay never passes through the buffer, so answered questions stay
// handled.
func (m *Model) appendBufferedAsyncQuestions(events []protocol.ThreadEvent) {
	if m == nil || len(events) == 0 {
		return
	}
	for _, event := range events {
		if event.Type != "item.completed" || event.Item == nil {
			continue
		}
		item := event.Item
		// Rust #46486: a committed reply envelope from another client resolves
		// the questions it names before any late question event arrives.
		if strings.EqualFold(strings.TrimSpace(item.Type), "user_message") ||
			strings.EqualFold(strings.TrimSpace(item.Type), "userMessage") {
			m.resolveAsyncQuestionsFromReplyText(firstNonEmpty(item.Text, item.Message))
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(item.Type), "agent_message") {
			continue
		}
		questions := bottompane.ParseAsyncUserInputQuestions(item.Metadata["questions"])
		if len(questions) == 0 {
			continue
		}
		m.asyncQuestions.AppendAt(item.ID, questions, m.currentTime())
	}
}

// asyncQuestionChoicesVisible reports whether the focused question's suggested
// choices fit the rows available below the transcript. Rust blocks submitting a
// choice that the terminal cannot display in full (#42894).
func (m *Model) asyncQuestionChoicesVisible() bool {
	question, ok := m.asyncQuestions.CurrentQuestion()
	if !ok || len(question.Options) == 0 {
		return true
	}
	required := m.asyncQuestionBlockRows(question)
	available := m.height - 3 - minTranscriptHeight
	if m.regionChromeEnabled() {
		available -= 3
	}
	return required <= max(available, 1)
}

// asyncQuestionBlockRows counts the rows the expanded editor needs for the
// question, its choices, the progress label, and the composer line.
func (m *Model) asyncQuestionBlockRows(question bottompane.AsyncUserInputQuestion) int {
	width := max(m.width-2, 1)
	rows := 0
	rows += len(wrapAsyncQuestionText(question.Title, width, 0))
	for index, label := range question.Options {
		rows += len(wrapAsyncQuestionText(label, width, asyncQuestionChoicePrefixWidth(index)))
	}
	rows += len(wrapAsyncQuestionText(m.asyncQuestions.OtherLabel(), width, asyncQuestionChoicePrefixWidth(len(question.Options))))
	if m.asyncQuestions.UnansweredCount() > 1 {
		rows++
	}
	rows++ // composer line
	return rows
}

// renderAsyncQuestions renders the collapsed summary or the expanded question
// and its choices above the composer.
func (m *Model) renderAsyncQuestions() []string {
	if !m.asyncQuestionEditorActive() {
		return nil
	}
	count := m.asyncQuestions.UnansweredCount()
	if !m.asyncQuestions.Expanded() {
		line := "  " + m.dimAsyncQuestionText("?") + " " +
			m.accentAsyncQuestionText(questionCountLabel(count))
		if countdown, ok := m.asyncQuestions.Countdown(m.currentTime()); ok {
			line += m.dimAsyncQuestionText(" · " + countdown)
		}
		lines := []string{line}
		// Rust #42903 keeps the edit binding on its own line so the countdown
		// never crowds it out.
		if binding := m.resolveAsyncQuestionBinding("chat", "edit_queued_message", "alt+↑"); binding != "" {
			lines = append(lines, m.dimAsyncQuestionText("    "+binding+" to answer"))
		}
		return lines
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
	for _, line := range wrapAsyncQuestionText(question.Title, width, 0) {
		lines = append(lines, m.accentAsyncQuestionText(line))
	}
	if len(question.Options) > 0 {
		lines = append(lines, m.renderAsyncQuestionChoices(question, width)...)
	}
	if hint := m.asyncQuestionHintLine(); hint != "" {
		lines = append(lines, hint)
	}
	return lines
}

// renderAsyncQuestionChoices renders the numbered suggested options plus the
// appended editable Other choice (Rust #42894/#42897).
func (m *Model) renderAsyncQuestionChoices(question bottompane.AsyncUserInputQuestion, width int) []string {
	selected := m.asyncQuestions.SelectedOptionIndex()
	labels := make([]string, 0, len(question.Options)+1)
	labels = append(labels, question.Options...)
	labels = append(labels, m.asyncQuestions.OtherLabel())
	lines := make([]string, 0, len(labels))
	for index, label := range labels {
		marker := " "
		if index == selected {
			marker = "›"
		}
		prefix := marker + " " + strconv.Itoa(index+1) + ". "
		prefixWidth := len([]rune(prefix))
		for lineIndex, line := range wrapAsyncQuestionText(label, width, prefixWidth) {
			text := line
			if lineIndex == 0 {
				text = prefix + line
			}
			if index == selected {
				lines = append(lines, m.accentAsyncQuestionText(text))
			} else {
				lines = append(lines, text)
			}
		}
	}
	return lines
}

// asyncQuestionHintLine renders the contextual submit/skip/navigation hints
// shown under the expanded question (Rust #42894).
func (m *Model) asyncQuestionHintLine() string {
	tips := []string{}
	if binding := m.resolveAsyncQuestionBinding("composer", "submit", "enter"); binding != "" {
		tips = append(tips, m.accentAsyncQuestionText(binding+" submit"))
	}
	if binding := m.resolveAsyncQuestionBinding("chat", "skip_question", "ctrl+]"); binding != "" {
		tips = append(tips, m.dimAsyncQuestionText(binding+" skip"))
	}
	if binding := m.resolveAsyncQuestionBinding("chat", "prompt_stack_back", "alt+↓"); binding != "" {
		label := "prev question"
		if m.asyncQuestions.CurrentIndex() == 0 {
			label = "main prompt"
		}
		tips = append(tips, m.dimAsyncQuestionText(binding+" "+label))
	}
	if m.asyncQuestions.CurrentIndex()+1 < m.asyncQuestions.UnansweredCount() {
		if binding := m.resolveAsyncQuestionBinding("chat", "edit_queued_message", "alt+↑"); binding != "" {
			tips = append(tips, m.dimAsyncQuestionText(binding+" next question"))
		}
	}
	return strings.Join(tips, "   ")
}

// resolveAsyncQuestionBinding resolves the configured display label for a
// keymap action, falling back to the default label when no keymap is loaded.
func (m *Model) resolveAsyncQuestionBinding(context string, action string, fallback string) string {
	if m == nil || m.keymapConfig == nil {
		return fallback
	}
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, context, action)
	if len(bindings) == 0 {
		return ""
	}
	return displayKeyBinding(bindings[0])
}

// asyncQuestionChoicePrefixWidth is the width of the "› N. " gutter used to
// wrap a choice label.
func asyncQuestionChoicePrefixWidth(index int) int {
	return len([]rune("› " + strconv.Itoa(index+1) + ". "))
}

// wrapAsyncQuestionText wraps text to width, indenting continuation lines by
// indent runes so wrapped choices keep their hanging indent.
func wrapAsyncQuestionText(text string, width int, indent int) []string {
	if width <= indent {
		width = indent + 1
	}
	style := lipgloss.NewStyle().Width(width - indent)
	out := []string{}
	for _, paragraph := range strings.Split(text, "\n") {
		wrapped := style.Render(paragraph)
		for index, line := range strings.Split(wrapped, "\n") {
			line = strings.TrimRight(line, " ")
			if index > 0 && indent > 0 {
				line = strings.Repeat(" ", indent) + strings.TrimLeft(line, " ")
			}
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func questionCountLabel(count int) string {
	if count == 1 {
		return "1 question"
	}
	return strconv.Itoa(count) + " questions"
}

func displayKeyBinding(binding string) string {
	parts := strings.Split(strings.TrimSpace(binding), "-")
	if len(parts) == 0 {
		return binding
	}
	// Rust #46680 renders one compact shortcut per binding: the control, shift
	// and alt modifiers in that order, joined with a bare `+`, then the key.
	var hasCtrl, hasShift, hasAlt bool
	key := ""
	for _, part := range parts {
		switch strings.ToLower(part) {
		case "":
			continue
		case "ctrl", "control":
			hasCtrl = true
		case "alt", "option":
			hasAlt = true
		case "shift":
			hasShift = true
		default:
			key = keyDisplayName(part)
		}
	}
	var label strings.Builder
	if hasCtrl {
		label.WriteString("ctrl+")
	}
	if hasShift {
		label.WriteString("shift+")
	}
	if hasAlt {
		label.WriteString(codextui.AltKeyLabel())
		label.WriteString("+")
	}
	label.WriteString(key)
	return label.String()
}

// keyDisplayName renders the key itself with Rust's names: the arrow glyphs,
// the named keys, and lower-case letters.
func keyDisplayName(key string) string {
	switch strings.ToLower(key) {
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "left":
		return "←"
	case "right":
		return "→"
	case "esc":
		return "esc"
	case "enter":
		return "enter"
	case "tab":
		return "tab"
	case "space":
		return "space"
	case "backspace":
		return "backspace"
	}
	return strings.ToLower(key)
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
