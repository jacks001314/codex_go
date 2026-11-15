package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/tui/keymsg"
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
	if !readOnly {
		m.leaveReadOnlyThread()
		return
	}
	m.enterReadOnlyThread(false)
}

// setArchivedThread enters or leaves the archived read-only state (Rust
// ChatWidget::show_read_only_thread(ReadOnlyThread::Archived), #61486). The
// cached transcript, drafts, pending steers, and question answers stay intact
// while the thread is frozen.
func (m *Model) setArchivedThread(archived bool) {
	if m == nil {
		return
	}
	if !archived {
		m.leaveReadOnlyThread()
		return
	}
	m.enterReadOnlyThread(true)
}

func (m *Model) enterReadOnlyThread(archived bool) {
	if m == nil || (m.readOnlyThread && m.archivedThread == archived) {
		return
	}
	if !m.readOnlyThread {
		// Rust #43253/#47320: the locked view hides the composer but keeps the
		// draft, so a successful retry or a fork (#47185) can continue with it.
		m.readOnlyDraft = m.captureComposerInputState()
	}
	m.readOnlyThread = true
	m.archivedThread = archived
	m.threadUnavailable = false
	if m.State != nil {
		m.freezeViewedReadReceipt(m.State.ThreadID)
	}
	// Block input while preserving the transcript; drafts and queued messages
	// survive for a successful retry or unarchive. resetComposer also dismisses
	// transient composer searches (Rust #61486).
	m.resetComposer()
	m.attachments = nil
	m.composerMentionBindings = nil
	m.composerPasteEnterUntil = nil
	m.notice = ""
}

func (m *Model) leaveReadOnlyThread() {
	if m == nil {
		return
	}
	m.frozenReadReceipt = nil
	m.readOnlyThread = false
	m.archivedThread = false
	if draft := m.readOnlyDraft; draft != nil {
		m.readOnlyDraft = nil
		m.restoreComposerInputState(draft, false)
	}
}

// externalWriterView mirrors Rust ChatWidget::is_external_writer_view: the
// conversation is locked because another app owns it. A provider-error lock
// (#58202) keeps the chat readable for a different reason and is not an
// external-writer view, so Escape navigation must not treat it as locked
// (#47320).
func (m *Model) externalWriterView() bool {
	return m != nil && m.readOnlyThread && !m.archivedThread && m.providerError == nil
}

// archivedView mirrors Rust ChatWidget::is_archived_view: the conversation is
// frozen because it was archived, so only U (unarchive) recovers it.
func (m *Model) archivedView() bool {
	return m != nil && m.readOnlyThread && m.archivedThread
}

// composerInputState is the retained input of a locked conversation (Rust
// capture_thread_input_state, chatwidget/input_restore.rs:607-654). Rust keeps
// the whole ThreadComposerState, so the draft's images, mentions and text
// elements survive the lock as well; Go's queued prompts live on the model
// instead of the composer, so only the composer-owned state is captured here.
type composerInputState struct {
	threadID        string
	text            string
	prefix          composerPrefix
	attachments     []bottompane.ComposerAttachment
	mentionBindings []string
	textElements    []ComposerTextElement
}

// captureComposerInputState snapshots a non-empty draft for its thread.
func (m *Model) captureComposerInputState() *composerInputState {
	if m == nil {
		return nil
	}
	if m.composerIsEmpty() && len(m.attachments) == 0 && len(m.composerMentionBindings) == 0 {
		return nil
	}
	return &composerInputState{
		threadID:        strings.TrimSpace(m.currentThreadID()),
		text:            m.composerBody(),
		prefix:          m.composerPrefix,
		attachments:     cloneComposerAttachments(m.attachments),
		mentionBindings: append([]string(nil), m.composerMentionBindings...),
		textElements:    cloneComposerTextElements(m.composerElements),
	}
}

// restoreComposerInputState puts a retained draft back once the composer is
// free. Without force the conversation must still be the one that retained it,
// so a retry of the same thread restores and a navigation elsewhere does not.
func (m *Model) restoreComposerInputState(state *composerInputState, force bool) bool {
	if m == nil || state == nil {
		return false
	}
	if state.text == "" && state.prefix == composerPrefixNone && len(state.attachments) == 0 {
		return false
	}
	if !force && strings.TrimSpace(m.currentThreadID()) != state.threadID {
		return false
	}
	if !m.composerIsEmpty() {
		return false
	}
	m.composerPrefix = state.prefix
	m.composerElements = cloneComposerTextElements(state.textElements)
	m.composer.SetValue(state.text)
	m.attachments = cloneComposerAttachments(state.attachments)
	m.composerMentionBindings = append([]string(nil), state.mentionBindings...)
	m.refreshComposerPrompt()
	return true
}

// commandCenterAvailable mirrors Rust's AppServerTarget::supports_command_center
// (#57281/#58238): a session connected to a local daemon, a remote App Server,
// or an embedded app server that provides dashboard actions can return to the
// command center.
func (m *Model) commandCenterAvailable() bool {
	if m == nil || m.onAgentsOverviewRefresh == nil {
		return false
	}
	if m.agentsOverviewEmbedded {
		return true
	}
	return m.localDaemonSession || m.remoteAppServer
}

// agentsNavigationKeyAvailable mirrors Rust BottomPane::agents_navigation_key_available
// (#48132/#41917): `Left` must still move the composer's cursor for the active
// editor/Vim keymap, and no global/chat/voice action may claim it. Go's keymap
// has no configurable chord prefixes, so Rust's chord-prefix exclusion has no
// counterpart here.
func (m *Model) agentsNavigationKeyAvailable() bool {
	if m == nil {
		return false
	}
	// Rust resolves the composer's move_left binding for the active keymap.
	// Vim normal mode uses its own keymap; insert mode and the plain composer
	// use the editor keymap, so an explicit editor remap keeps Left local.
	if m.vimMode && !m.vimInsert {
		bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "vim_normal", "move_left")
		if !keymapBindingsContainKey(bindings, "left") {
			return false
		}
	} else {
		bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "editor", "move_left")
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
func (m *Model) handleReadOnlyThreadKey(msg keymsg.KeyMsg, keySpec string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.commandCenterAvailable() {
		// Rust app/input.rs:600-605 accepts Left only as a plain press with no
		// modifiers, so Alt+Left (the composer agent-navigation chord) must not
		// fall through to the command center here.
		if msg.Type == keymsg.KeyEsc || (PlainKey(keymsg.KeyLeft).IsPress(msg) && m.agentsNavigationKeyAvailable()) {
			return m.applyAgentsCommand()
		}
	} else if msg.Type == keymsg.KeyEsc {
		return bubbletea.Quit
	}
	switch msg.Type {
	case keymsg.KeyCtrlC, keymsg.KeyCtrlD:
		return bubbletea.Quit
	}
	if m.keyMatches("global", "open_transcript", keySpec) {
		return m.openTranscriptOverlay()
	}
	if m.keyMatches("global", "find_transcript", keySpec) {
		// Rust #46734 (owned_transcript.rs:633): the locked transcript keeps the
		// find-transcript shortcut and opens the pager with its search bar.
		return m.openTranscriptFind()
	}
	if m.keyMatches("global", "focus_activity", keySpec) {
		m.focusTranscriptActivity()
		return nil
	}
	if m.keyMatches("global", "toggle_raw_output", keySpec) {
		return m.toggleRawOutputMode()
	}
	if m.keyMatches("global", "copy", keySpec) {
		m.copyLastAgentResponse()
		return nil
	}
	if msg.Type == keymsg.KeyRunes && len(msg.Runes) == 1 {
		switch msg.Runes[0] {
		case 'f', 'F':
			// Rust's locked-thread fork shortcut: f/F (plain or shifted) opens an
			// editable copy of the thread without taking the source lease. Ctrl
			// and Super combinations arrive as their own key types, so a rune
			// without Alt is the unmodified (or shifted) press Rust accepts, and a
			// blocking view keeps ownership of the key. #61486 keeps the shortcut
			// on the external-writer view only.
			if m.externalWriterView() && !msg.Alt && m.modal == nil {
				return m.applyForkCurrentSession("")
			}
		case 'r', 'R':
			// The archived view advertises U instead of retrying a resume the
			// server keeps rejecting (Rust app/input.rs).
			if !m.archivedView() {
				return m.retryReadOnlyThread()
			}
		case 'u', 'U':
			if m.archivedView() {
				return m.unarchiveArchivedThread()
			}
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
		Kind:      codextui.SessionSelectionResume,
		Target:    codextui.SessionTarget{BackendID: m.backendID, ThreadID: threadID, CWD: m.State.CWD},
		ResumeCWD: m.State.CWD,
	})
	if err != nil {
		// Rust #61486: a resume that reports the archived thread turns the
		// cached view into the archived read-only notice instead of a bare
		// retry failure.
		if _, archived := archivedSessionGuidanceForThread(err, threadID); archived {
			m.setArchivedThread(true)
			m.refreshTranscript()
			return nil
		}
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

// unarchiveArchivedThread handles U in the archived view: unarchive the
// displayed thread, then reattach it (Rust #61486 app/input.rs). A failed
// unarchive keeps the archived view and its retained draft.
func (m *Model) unarchiveArchivedThread() bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		return nil
	}
	if m.onUnarchiveThread == nil {
		m.notice = "Failed to unarchive thread: unarchiving is unavailable for this session"
		m.refreshTranscript()
		return nil
	}
	if err := m.onUnarchiveThread(threadID); err != nil && !activeWriterError(err) && !alreadyUnarchivedThreadError(err, threadID) {
		m.notice = "Failed to unarchive thread: " + strings.TrimSpace(err.Error())
		m.refreshTranscript()
		return nil
	}
	return m.resumeArchivedThread(threadID)
}

// resumeArchivedThread reattaches a thread after another client unarchived it
// or U restored it. A writer conflict or recovery failure leaves the archived
// view (and its draft) in place.
func (m *Model) resumeArchivedThread(threadID string) bubbletea.Cmd {
	if m == nil || m.State == nil || m.onResumeSession == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	response, err := m.onResumeSession(codextui.SessionSelection{
		Kind:      codextui.SessionSelectionResume,
		Target:    codextui.SessionTarget{BackendID: m.backendID, ThreadID: threadID, CWD: m.State.CWD},
		ResumeCWD: m.State.CWD,
	})
	if err != nil {
		// Rust keeps the archive flag when resume could not replace the view.
		m.notice = "Failed to resume thread: " + strings.TrimSpace(err.Error())
		m.refreshTranscript()
		return nil
	}
	if response.Summary != nil {
		m.upsertSessionItem(*response.Summary)
	}
	delete(m.archivedAgentThreads, threadID)
	m.applyResumeResponse(threadID, response)
	return nil
}

// alreadyUnarchivedThreadError reports the server's missing-archive rejection:
// another client may have unarchived the thread before its notification arrived.
func alreadyUnarchivedThreadError(err error, threadID string) bool {
	if err == nil || strings.TrimSpace(threadID) == "" {
		return false
	}
	return strings.Contains(err.Error(), "no archived rollout found for thread id "+strings.TrimSpace(threadID))
}

// activeWriterError reports the server's "another client owns the thread"
// rejection. Rust proceeds to resume on that error and lets the resume surface
// the writer conflict without clearing the archive flag.
func activeWriterError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "active writer") || strings.Contains(text, "another client")
}

// renderReadOnlyThreadNotice renders the read-only card in place of the
// composer (Rust ReadOnlyThreadNotice).
func (m *Model) renderReadOnlyThreadNotice() string {
	if m == nil {
		return ""
	}
	// Rust #47185: a pending fork replaces the locked notice with a progress
	// line inset by one row and two columns, and the keys stay blocked.
	if m.forkInProgress {
		return lipgloss.NewStyle().Padding(1, 2).Render(m.readOnlyDimText("Forking conversation\u2026"))
	}
	width := max(m.width, 20)
	contentWidth := max(width-4, 10)
	// Rust #61486: the archived card drops the lock prefix and retry wording;
	// only the recovery key changes ownership of the footer.
	title := "\U0001f512  This conversation is open in another app"
	hint := "r to retry"
	hintStyled := m.readOnlyAccentText("r") + " to retry"
	message := "Close it there and press R to continue here."
	indent := "    "
	if m.archivedThread {
		title = "This thread was archived"
		// Rust #47112: the hint is lowercase and only the key itself carries the
		// accent style.
		hint = "u to unarchive"
		hintStyled = m.readOnlyAccentText("u") + " to unarchive"
		message = "Press U to unarchive this thread and continue here."
		indent = ""
	}
	lines := codextui.AdaptiveWrapLine(title, codextui.WrapOptions{
		Width:      contentWidth,
		BreakWords: true,
	})
	if len(lines) == 0 {
		lines = []string{title}
	}
	if len(lines) == 1 && codextui.DisplayWidth(lines[0])+codextui.DisplayWidth(hint)+2 <= contentWidth {
		gap := contentWidth - codextui.DisplayWidth(lines[0]) - codextui.DisplayWidth(hint)
		lines[0] = lines[0] + strings.Repeat(" ", gap) + hintStyled
	} else {
		lines = append(lines, hintStyled)
	}
	lines = append(lines, codextui.AdaptiveWrapLine(
		message,
		codextui.WrapOptions{
			Width:            contentWidth,
			InitialIndent:    indent,
			SubsequentIndent: indent,
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

// readOnlyFooterLine mirrors Rust ReadOnlyThreadNotice::footer_lines: the
// recovery key (retry for an external writer, unarchive for an archived
// thread), the command-center key when one is available (Left/Esc, or Esc once
// Left was remapped), the exit keys, and the transcript shortcut.
func (m *Model) readOnlyFooterLine() string {
	items := []string{"r retry", "f fork"}
	if m.archivedThread {
		items = []string{"u unarchive"}
	}
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
