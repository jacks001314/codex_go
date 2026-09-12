package tea

// Archived-conversation confirmation prompt, porting Rust
// tui/src/unarchive_prompt.rs + the archived half of tui/src/session_start.rs.
// When resuming or forking a conversation the server reports as archived, the
// TUI first offers to unarchive it and retry instead of failing the startup.

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// unarchiveChoice mirrors Rust's UnarchiveChoice.
type unarchiveChoice int

const (
	unarchiveChoiceUnarchive unarchiveChoice = iota
	unarchiveChoiceCancel
	unarchiveChoiceQuit
)

// unarchivePromptState mirrors Rust's UnarchivePrompt.
type unarchivePromptState struct {
	threadID    string
	verb        string
	highlighted unarchiveChoice
}

func newUnarchivePromptState(threadID, verb string) *unarchivePromptState {
	return &unarchivePromptState{
		threadID:    strings.TrimSpace(threadID),
		verb:        strings.TrimSpace(verb),
		highlighted: unarchiveChoiceUnarchive,
	}
}

// handleKey ports Rust UnarchivePrompt::handle_key: ctrl+c/ctrl+d quit,
// up/down/k/j toggle the highlight, 1/y/Y confirm, esc/2/n/N cancel, and enter
// accepts the highlighted choice. (bubbletea reports no release events, which
// Rust ignores anyway.)
func (s *unarchivePromptState) handleKey(message bubbletea.KeyMsg) (unarchiveChoice, bool) {
	if s == nil {
		return unarchiveChoiceCancel, true
	}
	switch message.Type {
	case bubbletea.KeyCtrlC, bubbletea.KeyCtrlD:
		return unarchiveChoiceQuit, true
	case bubbletea.KeyUp, bubbletea.KeyDown:
		s.toggleHighlight()
		return 0, false
	case bubbletea.KeyEnter:
		return s.highlighted, true
	case bubbletea.KeyEsc:
		return unarchiveChoiceCancel, true
	case bubbletea.KeyRunes:
		key := string(message.Runes)
		if key == "" {
			return 0, false
		}
		switch key[0] {
		case 'k', 'j':
			s.toggleHighlight()
			return 0, false
		case '1', 'y', 'Y':
			return unarchiveChoiceUnarchive, true
		case '2', 'n', 'N':
			return unarchiveChoiceCancel, true
		}
	}
	return 0, false
}

func (s *unarchivePromptState) toggleHighlight() {
	if s == nil {
		return
	}
	if s.highlighted == unarchiveChoiceUnarchive {
		s.highlighted = unarchiveChoiceCancel
		return
	}
	s.highlighted = unarchiveChoiceUnarchive
}

// render ports Rust UnarchivePrompt::content: the archive notice, the thread id,
// the "Unarchive and <verb>" / "Cancel" selection rows, and the key hint.
func (s *unarchivePromptState) render() string {
	if s == nil {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("This conversation is archived")
	builder.WriteString("\n")
	builder.WriteString(s.threadID)
	builder.WriteString("\n\n")
	options := []struct {
		label  string
		choice unarchiveChoice
	}{
		{label: "Unarchive and " + s.verb, choice: unarchiveChoiceUnarchive},
		{label: "Cancel", choice: unarchiveChoiceCancel},
	}
	for index, option := range options {
		selected := s.highlighted == option.choice
		line := codextui.NumberedSelectionPrefix(index, selected) + option.label
		if selected {
			line = codextui.RenderSelectedRow(line)
		}
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
	builder.WriteString("Press enter to continue or esc to cancel")
	return strings.TrimRight(builder.String(), "\n")
}

// archivedSessionGuidance ports Rust session_start::archived_session_guidance:
// it extracts the server's "session <id> is archived. Run `codex unarchive
// <id>` ..." guidance from an error, dropping any trailing JSON-RPC code.
func archivedSessionGuidance(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	text := err.Error()
	index := strings.Index(text, "session ")
	if index < 0 {
		return "", false
	}
	message := text[index:]
	if !strings.Contains(message, " is archived. Run `codex unarchive ") {
		return "", false
	}
	if cut := strings.Index(message, " (code "); cut >= 0 {
		message = message[:cut]
	}
	return message, true
}

// archivedSessionGuidanceForThread mirrors Rust complete_session_start's guard:
// the guidance must name the requested thread, so an unrelated startup failure
// can never trigger an unarchive.
func archivedSessionGuidanceForThread(err error, threadID string) (string, bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", false
	}
	guidance, ok := archivedSessionGuidance(err)
	if !ok {
		return "", false
	}
	if !strings.HasPrefix(guidance, "session "+threadID+" is archived. ") {
		return "", false
	}
	return guidance, true
}

// openUnarchivePrompt turns the current session-picker modal into the archived
// conversation confirmation, keeping the same modal pointer so the picker's
// terminal mode is not disturbed (Rust unarchive_prompt.rs). It reports whether
// a modal was available to host the prompt.
func (m *Model) openUnarchivePrompt(threadID, verb string, retry codextui.SessionSelection) bool {
	if m == nil || m.modal == nil {
		return false
	}
	modal := m.modal
	modal.kind = ModalKindPicker
	modal.title = ""
	modal.body = ""
	modal.options = nil
	modal.selected = 0
	modal.footerNote = ""
	modal.footerHint = "Press enter to continue or esc to cancel"
	modal.sessionPicker = nil
	modal.sessionAction = nil
	modal.modelPicker = nil
	modal.modelReasoning = nil
	modal.planReasoningScope = nil
	modal.exitAfterSessionAction = false
	modal.unarchivePrompt = newUnarchivePromptState(threadID, verb)
	retried := retry
	modal.retrySelection = &retried
	m.notice = ""
	return true
}

// updateUnarchivePromptModal handles the archived confirmation keys and, on
// confirmation, unarchives the thread and retries the original resume/fork.
func (m *Model) updateUnarchivePromptModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.unarchivePrompt == nil {
		return nil
	}
	modal := m.modal
	prompt := modal.unarchivePrompt
	retry := modal.retrySelection
	choice, done := prompt.handleKey(message)
	if !done {
		return nil
	}
	threadID := prompt.threadID
	m.modal = nil
	switch choice {
	case unarchiveChoiceQuit:
		return bubbletea.Quit
	case unarchiveChoiceCancel:
		// Rust returns to the command center for a daemon/remote target and exits
		// for an embedded server; Go's embedded TUI exits and the remote TUI opens
		// the agents overview the same way its /agents command does.
		if m.localSession {
			return bubbletea.Quit
		}
		return m.applyAgentsCommand()
	}
	if _, err := m.runSessionAction(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionUnarchive,
		Target: codextui.SessionTarget{ThreadID: threadID},
	}); err != nil {
		m.addErrorHistoryMessage(err.Error())
		m.refreshTranscript()
		return nil
	}
	m.setSessionArchived(threadID, false)
	if retry == nil {
		return nil
	}
	// Rust retries the start by id after unarchiving, never by the moved rollout
	// path.
	decision, notice, _ := m.applySessionSelection(*retry)
	if decision == nil {
		if strings.TrimSpace(notice) != "" {
			m.notice = strings.TrimSpace(notice)
		}
		return nil
	}
	response := ModalResponse{
		ID:       "unarchive-prompt",
		Kind:     ModalKindPicker,
		OptionID: decision.Value,
		Picker:   decision,
	}
	var callback bubbletea.Cmd
	if m.onModalResponse != nil {
		callback = m.onModalResponse(response)
	}
	return m.closeSessionPickerTerminalMode(callback)
}
