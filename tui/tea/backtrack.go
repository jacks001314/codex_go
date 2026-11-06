package tea

import (
	"errors"
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
)

// This file wires the backtrack ("edit an earlier prompt") state machine ported
// from Rust tui/src/app_backtrack.rs into the bubbletea model: the first Esc
// primes the feature, the second opens the transcript overlay with the newest
// user message highlighted, Esc/Left and Right step the selection, and Enter
// reverts before the selected turn and restores its prompt in the composer.

// NoPreviousMessageToEdit mirrors Rust NO_PREVIOUS_MESSAGE_TO_EDIT.
const NoPreviousMessageToEdit = "No previous message to edit."

// UnsafePromptRestoreError rejects a remote prompt containing local image
// paths or command syntax that cannot be replayed safely (Rust #46739).
type UnsafePromptRestoreError struct {
	Message string
}

func (e UnsafePromptRestoreError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return "This remote prompt cannot be restored safely. Write a new message and reattach any images."
}

// PromptEditFunc reverts before a selected transcript prompt and returns the
// retained conversation so the model can attach to it (Rust
// RevertSessionForPromptEdit). The model restores the selection into the composer.
type PromptEditFunc func(selection tuiapp.PromptEditSelection) (SessionResumeResponse, error)

// transcriptBrowsingOrigin is the reading position and presentation to restore
// when Escape cancels prompt browsing (Rust transcript_view::TranscriptBookmark
// and BrowsingOrigin, #46739).
type transcriptBrowsingOrigin struct {
	overlay bool
	offset  int
	details bool
}

// applyBrowsingHints replaces the pager footer with the prompt-browsing footer.
func (m *Model) applyBrowsingHints() {
	if m == nil || m.overlay == nil {
		return
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	m.overlay.SetHints("Browsing", m.browsingActionHint(width))
}

// browsingActionHint picks the widest browsing hint row that fits, keeping the
// rewind and cancel actions visible on narrow terminals (Rust
// app_backtrack/prompt_navigation.rs).
func (m *Model) browsingActionHint(width int) string {
	details := m.openTranscriptHint()
	full := "↑↓/jk scroll · ←→/hl prompts"
	if details != "" {
		full += " · " + details + " details"
	}
	full += " · ↵ rewind · esc back"
	compact := "↑↓/jk ←→/hl"
	if details != "" {
		compact += " · " + details + " details"
	}
	compact += " · ↵ rewind · esc back"
	short := "↵ rewind · esc back"
	for _, candidate := range []string{full, compact, "↵ rewind · esc", short, "esc"} {
		if codextui.DisplayWidth(candidate) <= width {
			return candidate
		}
	}
	return "esc"
}

// restorePagerHints puts the normal transcript pager hint rows back after a
// browsing preview is dismissed.
func (m *Model) restorePagerHints() {
	if m == nil || m.overlay == nil {
		return
	}
	m.overlay.SetHints(
		"↑/↓ to scroll · pgup/pgdn to page · home/end to jump",
		m.pagerActionsHint(),
	)
}

// setBrowsingDetails switches the prompt browser between compact and detailed
// cells without leaving the preview or moving its selection.
func (m *Model) setBrowsingDetails(details bool) {
	if m == nil {
		return
	}
	m.browsingDetails = details
	m.syncTranscriptOverlay()
	m.refreshBacktrackHighlight()
}

// cancelTranscriptBrowsing dismisses browsing without rewinding. An existing
// overlay restores its bookmark; an overlay opened by the double-Escape gesture
// closes, leaving the owned transcript at its original reading position.
func (m *Model) cancelTranscriptBrowsing() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	origin := m.browsingOrigin
	m.browsingOrigin = nil
	if origin != nil && origin.overlay && m.overlay != nil {
		m.backtrack.Reset()
		m.escBacktrackHint = false
		m.overlay.ClearHighlightRange()
		m.browsingDetails = origin.details
		m.syncTranscriptOverlay()
		m.overlay.SetYOffset(origin.offset)
		m.restorePagerHints()
		return nil
	}
	m.browsingDetails = false
	cmd := m.closeTranscriptOverlay()
	if origin != nil && !origin.overlay {
		m.transcript.SetYOffset(origin.offset)
	}
	return cmd
}

// keyClaimedByPager reports whether an explicit pager binding already owns a
// key, so the h/l browsing aliases never override configured navigation.
func (m *Model) keyClaimedByPager(spec string) bool {
	if m == nil || spec == "" {
		return false
	}
	for _, action := range []string{
		"scroll_up", "scroll_down", "page_up", "page_down",
		"half_page_up", "half_page_down", "jump_top", "jump_bottom",
		"close", "close_transcript", "find",
	} {
		if m.keyMatches("pager", action, spec) {
			return true
		}
	}
	return false
}

// backtrackComposerEmpty mirrors Rust composer_is_empty for the backtrack gate:
// no draft text and no attachments.
func (m *Model) backtrackComposerEmpty() bool {
	if m == nil {
		return true
	}
	if !m.composerIsEmpty() {
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
	// Rust #47320 (app/input.rs): an external-writer view never enters prompt
	// backtracking; Escape belongs to session navigation instead.
	if m.externalWriterView() {
		return false
	}
	return tuiapp.ShouldHandleBacktrackEsc(m.inSideConversation(), m.normalBacktrackMode(), m.backtrackComposerEmpty(), m.shouldHandleVimInsertEscape())
}

// shouldRejectSideBacktrackEsc mirrors Rust should_reject_side_backtrack_esc.
func (m *Model) shouldRejectSideBacktrackEsc() bool {
	if m == nil {
		return false
	}
	return tuiapp.ShouldRejectSideBacktrackEsc(m.inSideConversation(), m.normalBacktrackMode(), m.backtrackComposerEmpty(), m.shouldHandleVimInsertEscape())
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
		return m.cancelTranscriptBrowsing()
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
	if m.browsingOrigin == nil {
		m.browsingOrigin = &transcriptBrowsingOrigin{}
	}
	m.browsingDetails = false
	m.backtrack.OverlayPreviewActive = true
	m.escBacktrackHint = false
	cmd := m.openTranscriptOverlay()
	m.applyBrowsingHints()
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
	offset := 0
	if m.overlay != nil {
		offset = m.overlay.YOffset()
	}
	m.browsingOrigin = &transcriptBrowsingOrigin{overlay: true, offset: offset, details: true}
	m.browsingDetails = false
	m.backtrack.Prime(m.currentThreadID())
	m.backtrack.OverlayPreviewActive = true
	m.syncTranscriptOverlay()
	m.applyBrowsingHints()
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
	_, ranges := renderTranscriptMessagesWithRanges(&m.overlayMessages, m.State, m.rawOutput, m.width, m.activeTUITheme(), m.browsingDetails, m.sessionCWD, m.showRawReasoning)
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

// applyBacktrackSelection reverts before the selected prompt and restores it in
// the composer (Rust apply_backtrack_selection /
// restore_backtrack_prompt_after_revert_error).
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
		m.restoreBacktrackPromptAfterRevertError(selection.Prompt, "prompt editing is unavailable")
		return nil
	}
	response, err := m.onPromptEdit(selection)
	if err != nil {
		var unsafe UnsafePromptRestoreError
		if errors.As(err, &unsafe) {
			m.addErrorHistoryMessage(unsafe.Error())
			m.refreshTranscript()
			return nil
		}
		message := strings.TrimSpace(err.Error())
		if message == "" {
			message = "unknown error"
		}
		m.restoreBacktrackPromptAfterRevertError(selection.Prompt, message)
		return nil
	}
	threadID := strings.TrimSpace(selection.ThreadID)
	if response.Summary != nil && strings.TrimSpace(response.Summary.ThreadID) != "" {
		threadID = strings.TrimSpace(response.Summary.ThreadID)
	}
	m.applyResumeResponse(threadID, response)
	m.applyHistoryCell(historycell.NewInfoEvent("Conversation reverted to this point. File changes are unchanged.", ""))
	m.restoreComposerPrompt(selection.Prompt)
	m.refreshTranscript()
	return nil
}

// restoreBacktrackPromptAfterRevertError restores the selected prompt and
// surfaces the revert failure (Rust restore_backtrack_prompt_after_revert_error).
func (m *Model) restoreBacktrackPromptAfterRevertError(prompt chatwidget.ThreadComposerState, err string) {
	if m == nil {
		return
	}
	m.restoreComposerPrompt(prompt)
	m.addErrorHistoryMessage("Failed to revert before the selected prompt: " + err)
	m.refreshTranscript()
}

// restoreComposerPrompt replaces the composer with the selected prompt's text
// and attachments (Rust restore_user_message_to_composer).
func (m *Model) restoreComposerPrompt(prompt chatwidget.ThreadComposerState) {
	if m == nil {
		return
	}
	m.clearComposerPasteWindow()
	m.setComposerPrompt(prompt.Text)
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
	elements := make([]ComposerTextElement, 0, len(prompt.TextElements))
	for _, element := range prompt.TextElements {
		start := int(element.ByteRange.Start)
		end := int(element.ByteRange.End)
		if start < 0 || end > len(prompt.Text) || start >= end {
			continue
		}
		placeholder := prompt.Text[start:end]
		if element.Placeholder != nil {
			placeholder = *element.Placeholder
		}
		elements = append(elements, ComposerTextElement{
			Start:       start,
			End:         end,
			Placeholder: placeholder,
		})
	}
	m.restoreComposerTextElements(prompt.Text, elements)
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
	m.browsingOrigin = nil
	m.browsingDetails = false
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
