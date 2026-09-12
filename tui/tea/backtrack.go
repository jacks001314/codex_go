package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
)

// This file wires the backtrack ("edit an earlier prompt") state machine ported
// from Rust tui/src/app_backtrack.rs into the bubbletea model: the first Esc
// primes the feature, the second opens the transcript overlay with the newest
// user message highlighted, Esc/Left and Right step the selection, and Enter
// branches before the selected turn and restores its prompt in the composer.

// NoPreviousMessageToEdit mirrors Rust NO_PREVIOUS_MESSAGE_TO_EDIT.
const NoPreviousMessageToEdit = "No previous message to edit."

// PromptEditFunc branches before a selected transcript prompt and returns the
// branched conversation so the model can attach to it (Rust
// ForkSessionForPromptEdit). The model restores the selection into the composer.
type PromptEditFunc func(selection tuiapp.PromptEditSelection) (SessionResumeResponse, error)

// backtrackComposerEmpty mirrors Rust composer_is_empty for the backtrack gate:
// no draft text and no attachments.
func (m *Model) backtrackComposerEmpty() bool {
	if m == nil {
		return true
	}
	if strings.TrimSpace(m.composer.Value()) != "" {
		return false
	}
	return len(m.attachments) == 0
}

// normalBacktrackMode mirrors Rust is_normal_backtrack_mode: backtracking only
// primes from the idle composer with no modal, popup, or expanded question
// editor in the way.
func (m *Model) normalBacktrackMode() bool {
	if m == nil {
		return false
	}
	if m.isTaskRunning() || m.asyncQuestions.Expanded() || m.modal != nil {
		return false
	}
	return !m.slashPopup.Active && !m.skillPopup.Active
}

// shouldHandleBacktrackEsc mirrors Rust should_handle_backtrack_esc.
func (m *Model) shouldHandleBacktrackEsc() bool {
	if m == nil {
		return false
	}
	return tuiapp.ShouldHandleBacktrackEsc(m.inSideConversation(), m.normalBacktrackMode(), m.backtrackComposerEmpty(), false)
}

// shouldRejectSideBacktrackEsc mirrors Rust should_reject_side_backtrack_esc.
func (m *Model) shouldRejectSideBacktrackEsc() bool {
	if m == nil {
		return false
	}
	return tuiapp.ShouldRejectSideBacktrackEsc(m.inSideConversation(), m.normalBacktrackMode(), m.backtrackComposerEmpty(), false)
}

// handleBacktrackEscKey mirrors Rust handle_backtrack_esc_key.
func (m *Model) handleBacktrackEscKey() bubbletea.Cmd {
	if m == nil || !m.backtrackComposerEmpty() {
		return nil
	}
	if !m.backtrack.Primed {
		m.primeBacktrack()
		return nil
	}
	if m.overlay == nil {
		return m.openBacktrackPreview()
	}
	if m.backtrack.OverlayPreviewActive {
		m.stepBacktrackAndHighlight(false)
	}
	return nil
}

// primeBacktrack arms backtrack mode and advertises the second-Esc hint when a
// user message can be reopened (Rust prime_backtrack).
func (m *Model) primeBacktrack() {
	if m == nil {
		return
	}
	m.backtrack.Prime(m.currentThreadID())
	if tuiapp.HasBacktrackTarget(m.State.Messages) {
		m.escBacktrackHint = true
	}
}

// rejectSideBacktrackEsc mirrors Rust reject_side_backtrack_esc.
func (m *Model) rejectSideBacktrackEsc() {
	if m == nil {
		return
	}
	m.resetBacktrackState()
	m.addErrorHistoryMessage(tuiapp.SideEditPreviousUnavailableMessage)
	m.refreshTranscript()
}

// openBacktrackPreview opens the transcript overlay and highlights the newest
// user message, or reports that nothing can be edited (Rust
// open_backtrack_preview).
func (m *Model) openBacktrackPreview() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if !tuiapp.HasBacktrackTarget(m.State.Messages) {
		m.resetBacktrackState()
		m.addInfoHistoryMessage(NoPreviousMessageToEdit)
		m.refreshTranscript()
		return nil
	}
	cmd := m.openTranscriptOverlay()
	m.backtrack.OverlayPreviewActive = true
	m.escBacktrackHint = false
	m.stepBacktrackAndHighlight(false)
	return cmd
}

// beginOverlayBacktrackPreview starts a preview from an already-open transcript
// overlay and selects the newest user message (Rust
// begin_overlay_backtrack_preview).
func (m *Model) beginOverlayBacktrackPreview() {
	if m == nil {
		return
	}
	if !tuiapp.HasBacktrackTarget(m.State.Messages) {
		m.closeTranscriptOverlay()
		m.addInfoHistoryMessage(NoPreviousMessageToEdit)
		m.refreshTranscript()
		return
	}
	m.backtrack.Prime(m.currentThreadID())
	m.backtrack.OverlayPreviewActive = true
	m.stepBacktrackAndHighlight(false)
}

// stepBacktrackAndHighlight moves the selection one message older (or newer)
// and re-highlights it in the overlay (Rust step_backtrack_and_highlight /
// step_forward_backtrack_and_highlight).
func (m *Model) stepBacktrackAndHighlight(forward bool) {
	if m == nil || m.State == nil {
		return
	}
	count := tuiapp.UserCount(m.State.Messages)
	if count == 0 {
		return
	}
	next := tuiapp.StepBackwardBacktrack(m.backtrack.NthUserMessage, count)
	if forward {
		next = tuiapp.StepForwardBacktrack(m.backtrack.NthUserMessage, count)
	}
	if _, ok := tuiapp.NthUserPosition(m.State.Messages, next); !ok {
		next = tuiapp.BacktrackNoSelection
	}
	m.backtrack.NthUserMessage = next
	m.refreshBacktrackHighlight()
}

// refreshBacktrackHighlight maps the selected user message onto its rendered
// transcript line range and highlights it in the overlay.
func (m *Model) refreshBacktrackHighlight() {
	if m == nil || m.overlay == nil || !m.overlayTranscript {
		return
	}
	if !m.backtrack.HasSelection() {
		m.overlay.ClearHighlightRange()
		return
	}
	index, ok := tuiapp.NthUserPosition(m.State.Messages, m.backtrack.NthUserMessage)
	if !ok {
		m.overlay.ClearHighlightRange()
		return
	}
	_, ranges := renderTranscriptMessagesWithRanges(&m.overlayMessages, m.State, m.rawOutput, m.width, m.activeTUITheme(), true, m.sessionCWD)
	if index >= len(ranges) {
		m.overlay.ClearHighlightRange()
		return
	}
	m.overlay.SetHighlightRange(ranges[index][0], ranges[index][1])
}

// confirmBacktrackFromMain confirms a primed selection when no overlay is open
// (Rust confirm_backtrack_from_main).
func (m *Model) confirmBacktrackFromMain() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	selection, ok := m.backtrack.BacktrackSelection(m.currentThreadID(), m.State.Messages)
	m.resetBacktrackState()
	if !ok {
		return nil
	}
	return m.applyBacktrackSelection(selection)
}

// confirmOverlayBacktrack confirms the highlighted selection and closes the
// overlay (Rust overlay_confirm_backtrack).
func (m *Model) confirmOverlayBacktrack() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	selection, ok := m.backtrack.BacktrackSelection(m.currentThreadID(), m.State.Messages)
	m.closeTranscriptOverlay()
	if !ok {
		return nil
	}
	return m.applyBacktrackSelection(selection)
}

// applyBacktrackSelection branches before the selected prompt and restores it in
// the composer (Rust apply_backtrack_selection /
// restore_backtrack_prompt_after_branch_error).
func (m *Model) applyBacktrackSelection(selection tuiapp.PromptEditSelection) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.inSideConversation() {
		m.resetBacktrackState()
		m.addErrorHistoryMessage(tuiapp.SideEditPreviousUnavailableMessage)
		m.refreshTranscript()
		return nil
	}
	if strings.TrimSpace(selection.ThreadID) != strings.TrimSpace(m.currentThreadID()) {
		return nil
	}
	if m.onPromptEdit == nil {
		m.restoreBacktrackPromptAfterBranchError(selection.Prompt, "prompt editing is unavailable")
		return nil
	}
	response, err := m.onPromptEdit(selection)
	if err != nil {
		message := strings.TrimSpace(err.Error())
		if message == "" {
			message = "unknown error"
		}
		m.restoreBacktrackPromptAfterBranchError(selection.Prompt, message)
		return nil
	}
	threadID := strings.TrimSpace(selection.ThreadID)
	if response.Summary != nil && strings.TrimSpace(response.Summary.ThreadID) != "" {
		threadID = strings.TrimSpace(response.Summary.ThreadID)
	}
	m.applyResumeResponse(threadID, response)
	m.restoreComposerPrompt(selection.Prompt)
	m.refreshTranscript()
	return nil
}

// restoreBacktrackPromptAfterBranchError restores the selected prompt and
// surfaces the branch failure (Rust restore_backtrack_prompt_after_branch_error).
func (m *Model) restoreBacktrackPromptAfterBranchError(prompt chatwidget.ThreadComposerState, err string) {
	if m == nil {
		return
	}
	m.restoreComposerPrompt(prompt)
	m.addErrorHistoryMessage("Failed to branch before the selected prompt: " + err)
	m.refreshTranscript()
}

// restoreComposerPrompt replaces the composer with the selected prompt's text
// and attachments (Rust restore_user_message_to_composer).
func (m *Model) restoreComposerPrompt(prompt chatwidget.ThreadComposerState) {
	if m == nil {
		return
	}
	m.clearComposerPasteWindow()
	m.composer.SetValue(prompt.Text)
	m.attachments = nil
	for _, path := range prompt.LocalImages {
		if strings.TrimSpace(path) == "" {
			continue
		}
		m.attachments = append(m.attachments, bottompane.ComposerAttachment{Kind: bottompane.AttachmentImage, Path: path})
	}
	for _, url := range prompt.RemoteImageURLs {
		if strings.TrimSpace(url) == "" {
			continue
		}
		m.attachments = append(m.attachments, bottompane.ComposerAttachment{Kind: bottompane.AttachmentRemoteImage, URL: url})
	}
	m.composerElements = nil
	for _, element := range prompt.TextElements {
		start := int(element.ByteRange.Start)
		end := int(element.ByteRange.End)
		placeholder := prompt.Text
		if element.Placeholder != nil {
			placeholder = *element.Placeholder
		}
		if start < 0 || end > len(prompt.Text) || start >= end {
			continue
		}
		if placeholder != "" && prompt.Text[start:end] != placeholder {
			continue
		}
		m.composerElements = append(m.composerElements, ComposerTextElement{Start: start, End: end, Placeholder: prompt.Text[start:end]})
	}
	m.extendComposerPasteWindow(m.currentTime())
	m.refreshSlashPopup()
}

// userPromptMessageState extracts the restorable prompt state from a submission
// so the transcript message can rebuild the composer draft when backtracking
// (Rust UserHistoryCell).
func userPromptMessageState(request SubmitRequest) (string, []string, []string, []codextui.MessageTextElement) {
	var localImages, remoteImages []string
	for _, attachment := range request.Attachments {
		switch attachment.Kind {
		case bottompane.AttachmentImage:
			if strings.TrimSpace(attachment.Path) != "" {
				localImages = append(localImages, attachment.Path)
			}
		case bottompane.AttachmentRemoteImage:
			if strings.TrimSpace(attachment.URL) != "" {
				remoteImages = append(remoteImages, attachment.URL)
			}
		}
	}
	var elements []codextui.MessageTextElement
	for _, element := range request.TextElements {
		elements = append(elements, codextui.MessageTextElement{Start: element.Start, End: element.End, Placeholder: element.Placeholder})
	}
	return strings.TrimSpace(request.Prompt), localImages, remoteImages, elements
}

// resetBacktrackState clears the state machine, the composer hint, and any
// overlay highlight (Rust reset_backtrack_state).
func (m *Model) resetBacktrackState() {
	if m == nil {
		return
	}
	m.backtrack.Reset()
	m.escBacktrackHint = false
	if m.overlay != nil {
		m.overlay.ClearHighlightRange()
	}
}

// currentThreadID returns the active thread id, tolerating a nil state.
func (m *Model) currentThreadID() string {
	if m == nil || m.State == nil {
		return ""
	}
	return strings.TrimSpace(m.State.ThreadID)
}
