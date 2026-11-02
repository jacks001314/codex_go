package tea

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	historycell "codex_go/tui/history_cell"
)

func (m *Model) applyResumeCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	query := strings.TrimSpace(args)
	if query == "" {
		return m.openSessionPicker(codextui.SessionPickerResume)
	}
	item, ok, ambiguous := m.findSessionByIDOrName(query)
	if ambiguous {
		m.notice = "Multiple saved chats match '" + query + "'."
		cmd := m.openSessionPicker(codextui.SessionPickerResume)
		if m.modal != nil && m.modal.sessionPicker != nil {
			m.modal.sessionPicker.Query = query
			m.modal.sessionPicker.SelectFirst()
		}
		return cmd
	}
	if !ok {
		message := "No saved chat found matching '" + query + "'."
		m.notice = message
		m.addHistoryCell(historycell.NewErrorEvent(message))
		m.refreshTranscript()
		return nil
	}
	selection := codextui.SessionSelection{
		Kind: codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{
			BackendID: m.backendID,
			Path:      item.Path,
			ThreadID:  item.ThreadID,
			CWD:       item.CWD,
		},
	}
	decision, notice, _ := m.applySessionSelection(selection)
	if strings.TrimSpace(notice) != "" {
		m.notice = notice
	}
	m.refreshTranscript()
	if decision == nil {
		if strings.TrimSpace(notice) != "" {
			m.addHistoryCell(historycell.NewErrorEvent(strings.TrimSpace(notice)))
			m.refreshTranscript()
		}
		return nil
	}
	if m.onModalResponse != nil {
		return m.onModalResponse(ModalResponse{
			ID:          "session-picker-resume",
			Kind:        ModalKindPicker,
			OptionID:    firstNonEmpty(item.ThreadID, item.Path),
			OptionLabel: item.DisplayTitle(),
			Picker:      decision,
		})
	}
	return nil
}

func (m *Model) findSessionByIDOrName(query string) (codextui.SessionSummary, bool, bool) {
	if m == nil {
		return codextui.SessionSummary{}, false, false
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return codextui.SessionSummary{}, false, false
	}
	lower := strings.ToLower(query)
	var exact []codextui.SessionSummary
	var prefix []codextui.SessionSummary
	for _, item := range m.sessionItems {
		fields := sessionLookupFields(item)
		for _, field := range fields {
			fieldLower := strings.ToLower(strings.TrimSpace(field))
			if fieldLower == "" {
				continue
			}
			if fieldLower == lower {
				exact = append(exact, item)
				break
			}
			if strings.HasPrefix(fieldLower, lower) {
				prefix = append(prefix, item)
				break
			}
		}
	}
	if len(exact) == 1 {
		return exact[0], true, false
	}
	if len(exact) > 1 {
		return codextui.SessionSummary{}, false, true
	}
	if len(prefix) == 1 {
		return prefix[0], true, false
	}
	if len(prefix) > 1 {
		return codextui.SessionSummary{}, false, true
	}
	return codextui.SessionSummary{}, false, false
}

func sessionLookupFields(item codextui.SessionSummary) []string {
	fields := []string{
		item.ThreadID,
		item.Title,
		item.Path,
	}
	if base := filepath.Base(strings.TrimSpace(item.Path)); base != "." && base != "" {
		fields = append(fields, base)
	}
	return fields
}

func (m *Model) openSessionPicker(action codextui.SessionPickerAction) bubbletea.Cmd {
	picker := codextui.NewSessionPickerStateForBackend(
		m.backendID,
		action,
		m.sessionItems,
		m.sessionCWD,
		// Rust #54175: the retired `worktrees` flag no longer gates linked
		// checkout discovery; the model's local/remote scope decides instead.
		codextui.SessionPickerWorktreeCWDs(m.sessionCWD, m.worktreesEnabled)...,
	)
	if picker == nil {
		// Rust #42419: a picker that cannot open reports its failure inside the
		// command center instead of silently staying on the task list.
		if m.agentsOverview != nil {
			m.addSessionPickerError("Failed to open session picker: no saved sessions are available.")
			return nil
		}
		m.notice = "No sessions available."
		return nil
	}
	// Rust #56308: Repo only appears for local sessions whose directory has
	// more than one canonical linked-worktree directory.
	picker.RepoFilterAvailable = m.worktreesEnabled && codextui.SessionPickerRepoFilterAvailable(m.sessionCWD)
	picker.ThemeID = m.tuiTheme
	picker.Density = m.sessionPickerDensity
	picker.UseThemeColors = m.statusLineUseColors
	pickerID := "session-picker-" + string(action)
	visible := picker.VisibleItems()
	if len(visible) == 0 {
		if m.onListSessionPickerItems == nil {
			m.notice = "No sessions available to " + action.Label() + "."
			return nil
		}
		// Rust #58280: the live backend listing may still find sessions even
		// though the startup snapshot was empty.
		picker.Loading = true
	}
	modalOptions := make([]ModalOption, 0, len(visible))
	for i, item := range visible {
		modalOptions = append(modalOptions, ModalOption{
			ID:          firstNonEmpty(item.ThreadID, item.Path),
			Label:       sessionPickerLabel(item),
			Description: sessionPickerDescription(item),
			Shortcut:    modelPickerShortcut(i),
		})
	}
	m.openModal(ModalRequestMsg{
		ID:      pickerID,
		Kind:    ModalKindPicker,
		Title:   action.Title(),
		Body:    "Choose a session to " + action.Label() + ".",
		Options: modalOptions,
	})
	if m.modal != nil {
		m.modal.sessionPicker = picker
		m.modal.sessionPickerNotice = strings.TrimSpace(m.sessionPickerLoadNotice)
		m.modal.selected = picker.Selected
	}
	var cmds []bubbletea.Cmd
	if refresh := m.sessionPickerRefreshCmd(pickerID); refresh != nil {
		cmds = append(cmds, refresh)
	}
	if m.noAltScreen {
		return bubbletea.Batch(cmds...)
	}
	m.sessionPickerAltScreen = true
	cmds = append(cmds, bubbletea.EnterAltScreen)
	return bubbletea.Batch(cmds...)
}

// addSessionPickerError mirrors Rust App::add_session_picker_error (#42419):
// while the command center is open the failure shows a dismissal view titled
// "Unable to resume session", and the message is also recorded as an error so
// the reason survives leaving the command center.
func (m *Model) addSessionPickerError(message string) {
	if m == nil {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "unknown error"
	}
	if m.agentsOverview != nil {
		m.openModal(ModalRequestMsg{
			ID:    "session-picker-error",
			Kind:  ModalKindPicker,
			Title: "Unable to resume session",
			Body:  message,
			Options: []ModalOption{{
				ID:    "return",
				Label: "Return to command center",
			}},
		})
	}
	m.addErrorHistoryMessage(message)
	m.refreshTranscript()
}

// applyResumePickerSelection mirrors Rust App::apply_resume_picker_selection
// (#42419). Switching tasks keeps the current composer draft in the per-thread
// input state, restores the resumed task's draft, and returns to the
// conversation when the resumed task already has a turn in flight. A cancelled
// picker never reaches this path, so the command center keeps its state.
func (m *Model) applyResumePickerSelection(selection codextui.SessionSelection) (*PickerDecision, string, bool) {
	if m == nil {
		return nil, "", true
	}
	if selection.Kind != codextui.SessionSelectionResume {
		// Rust refreshes in-memory configuration for the non-resume selections
		// and ignores Fork; the shared selection path already does that.
		return m.applySessionSelection(selection)
	}
	target := strings.TrimSpace(selection.Target.ThreadID)
	current := ""
	if m.State != nil {
		current = strings.TrimSpace(m.State.ThreadID)
	}
	switching := target != "" && target != current
	if switching {
		// Rust stores the current widget's input state before the switch so a
		// later return to this task restores what was being composed.
		m.captureAgentsOverviewDraft(current)
	}
	decision, notice, complete := m.applySessionSelection(selection)
	if !complete || decision == nil {
		// Resume failures stay inside the command-center flow: the caller shows
		// the notice and the task list is untouched.
		return decision, notice, complete
	}
	attached := ""
	if m.State != nil {
		attached = strings.TrimSpace(m.State.ThreadID)
	}
	if switching && attached != "" && attached == target {
		if draft, ok := m.agentsOverviewDrafts[target]; ok {
			m.setComposerPrompt(draft)
			delete(m.agentsOverviewDrafts, target)
		} else {
			// Rust builds a fresh chat widget when the task has no saved input
			// state, so the composer starts empty instead of carrying the
			// previous task's draft.
			m.resetComposer()
		}
	}
	if m.agentsOverview != nil && attached != "" && attached == target && m.isTaskRunning() {
		// Rust sets ViewCompletion::Accepted and redraws when the resumed task
		// has a turn in flight; an idle task keeps the command center open.
		m.agentsOverview.Completion = agentsoverview.CompletionAccepted
		m.closeAgentsOverview()
	}
	return decision, notice, complete
}

func (m *Model) openSessionActionConfirmation(selection codextui.SessionSelection) {
	action := string(selection.Kind)
	target := strings.TrimSpace(selection.Target.DisplayLabel())
	if target == "" {
		target = "selected session"
	}
	title := "Confirm session " + action
	body := "This will " + action + " " + target + "."
	if selection.Kind == codextui.SessionSelectionDelete {
		body += "\nThis cannot be undone."
	}
	copied := selection
	m.replaceModal(&modalState{
		id:            "session-action-" + action,
		kind:          ModalKindPicker,
		title:         title,
		body:          body,
		sessionAction: &copied,
		options: []ModalOption{
			{ID: "confirm", Label: titleCaseASCII(action), Description: target, Shortcut: "y"},
			{ID: "cancel", Label: "Cancel", Shortcut: "n"},
		},
	})
	m.notice = ""
}

func (m *Model) openCurrentSessionActionConfirmation(kind codextui.SessionSelectionKind) {
	if m == nil || m.State == nil {
		return
	}
	selection := codextui.SessionSelection{
		Kind: kind,
		Target: codextui.SessionTarget{
			BackendID: m.backendID,
			ThreadID:  strings.TrimSpace(m.State.ThreadID),
		},
	}
	title := "Archive this session?"
	body := "Are you sure? This will archive the current session and exit Codex"
	confirmLabel := "Yes, archive and exit"
	confirmDescription := "Archive this session now"
	// Rust #50764 (codex-rs/tui/src/chatwidget/slash_dispatch.rs):
	// /archive stays available while a turn is running, and the confirmation warns
	// that archiving stops the current turn. Cancelling leaves the turn running.
	if kind == codextui.SessionSelectionArchive && m.isTaskRunning() {
		body = "This will stop the current turn and archive the session."
	}
	if kind == codextui.SessionSelectionDelete {
		title = "Delete this session?"
		body = "Cannot be undone. Subagent threads will also be deleted."
		confirmLabel = "Yes, delete and exit"
		confirmDescription = "Permanently delete this session now"
		if m.sharedServerCommandCenterAvailable() {
			confirmLabel = "Yes, delete and return to command center"
		}
	}
	m.replaceModal(&modalState{
		id:                     "current-session-action-" + string(kind),
		kind:                   ModalKindPicker,
		title:                  title,
		body:                   body,
		sessionAction:          &selection,
		exitAfterSessionAction: true,
		options: []ModalOption{
			{ID: "cancel", Label: "No, keep this session", Description: "Return to the current session"},
			{ID: "confirm", Label: confirmLabel, Description: confirmDescription},
		},
	})
	m.notice = ""
}

func (m *Model) sharedServerCommandCenterAvailable() bool {
	return m != nil && m.commandCenterAvailable()
}

// movePendingSteersToFork carries uncommitted steers from a locked thread onto
// its fork (Rust #47185 restores pending_steers with the rest of the thread
// input state). switchThreadID parks them under the source thread; a copy there
// would steer the source conversation if the user navigated back, so the parked
// entry is dropped.
func (m *Model) movePendingSteersToFork(sourceThreadID string, forkThreadID string, steers []pendingSteerSubmission) {
	if m == nil || len(steers) == 0 {
		return
	}
	sourceThreadID = strings.TrimSpace(sourceThreadID)
	forkThreadID = strings.TrimSpace(firstNonEmpty(forkThreadID, m.currentThreadID()))
	if sourceThreadID == "" || forkThreadID == "" || sourceThreadID == forkThreadID {
		return
	}
	delete(m.threadPendingSteers, sourceThreadID)
	if strings.TrimSpace(m.currentThreadID()) != forkThreadID {
		return
	}
	m.pendingSteers = append(m.pendingSteers, steers...)
	m.pendingSteersThreadID = forkThreadID
}

// forkCurrentSessionMsg carries the result of a locked-view fork that ran off
// the update loop (Rust #47185 spawns the fork and repaints
// "Forking conversation…" while it is pending).
type forkCurrentSessionMsg struct {
	selection      codextui.SessionSelection
	sourceThreadID string
	retained       *composerInputState
	retainedSteers []pendingSteerSubmission
	fromLocked     bool
	summary        *codextui.SessionSummary
	err            error
}

func (m *Model) applyForkCurrentSession(name string) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	// Rust #47185: a pending fork blocks another fork request.
	if m.forkInProgress {
		return nil
	}
	// Rust #43340: a pending named-profile selection blocks forking so the new
	// thread cannot be created with the previous permissions.
	if strings.TrimSpace(m.pendingServerProfile) != "" {
		m.addErrorHistoryMessage("Wait for permissions to update before forking.")
		m.refreshTranscript()
		return nil
	}
	// Rust #47185: forking from the locked conversation keeps its local draft
	// and never takes the source lease.
	fromLocked := m.externalWriterView()
	retained := (*composerInputState)(nil)
	// Rust #47185 restores the locked thread's whole ThreadInputState, which
	// carries steer submissions that had not committed yet
	// (chatwidget/user_messages.rs:157 pending_steers). Go parks those under the
	// source thread when the fork switches threads, so they are captured here and
	// moved onto the fork afterwards.
	retainedSteers := []pendingSteerSubmission(nil)
	if fromLocked {
		retained = m.readOnlyDraft
		if retained == nil {
			retained = m.captureComposerInputState()
		}
		m.readOnlyDraft = nil
		retainedSteers = append(retainedSteers, m.pendingSteers...)
	}
	selection := codextui.SessionSelection{
		Kind: codextui.SessionSelectionFork,
		Target: codextui.SessionTarget{
			BackendID: m.backendID,
			ThreadID:  strings.TrimSpace(m.State.ThreadID),
		},
		Name: strings.TrimSpace(name),
	}
	if fromLocked {
		// Rust #47185: the locked view owns the keyboard for the duration of the
		// fork and paints "Forking conversation…" before the request is sent, so
		// the RPC must not run inside the key handler (that froze the whole UI
		// for as long as the backend took, and the progress line never appeared).
		m.forkInProgress = true
		m.refreshTranscript()
		action := m.onSessionAction
		msg := forkCurrentSessionMsg{
			selection:      selection,
			sourceThreadID: selection.Target.ThreadID,
			retained:       retained,
			retainedSteers: retainedSteers,
			fromLocked:     true,
		}
		return func() bubbletea.Msg {
			// The Cmd runs on its own goroutine and only reads captured values;
			// it never touches m, so quitting or switching threads mid-flight
			// cannot race with (or mutate) a torn-down model.
			if action == nil {
				return msg
			}
			msg.summary, msg.err = action(selection)
			return msg
		}
	}
	decision, notice, _ := m.applySessionSelection(selection)
	if strings.TrimSpace(notice) != "" {
		m.notice = strings.TrimSpace(notice)
	}
	if decision == nil {
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return nil
	}
	m.refreshTranscript()
	if m.onModalResponse != nil {
		return m.onModalResponse(ModalResponse{
			ID:       "fork-current-session",
			Kind:     ModalKindPicker,
			OptionID: selection.Target.ThreadID,
			Picker:   decision,
		})
	}
	return nil
}

// applyForkCurrentSessionResult finishes a locked-view fork that ran off the
// update loop: success clears the locked view and moves the draft (plus any
// pending steers) onto the fork, failure restores the draft and keeps the lock.
func (m *Model) applyForkCurrentSessionResult(msg forkCurrentSessionMsg) bubbletea.Cmd {
	if m == nil || !msg.fromLocked {
		return nil
	}
	m.forkInProgress = false
	if msg.err != nil {
		// Rust session_start.rs: forking an archived conversation prompts too.
		if _, ok := archivedSessionGuidanceForThread(msg.err, msg.sourceThreadID); ok &&
			m.openUnarchivePrompt(msg.sourceThreadID, "fork", msg.selection) {
			m.readOnlyDraft = msg.retained
			m.refreshTranscript()
			return nil
		}
		m.readOnlyDraft = msg.retained
		m.notice = strings.TrimSpace(msg.err.Error())
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return nil
	}
	decision, notice, _ := m.applyForkSummary(msg.selection, msg.summary)
	if strings.TrimSpace(notice) != "" {
		m.notice = strings.TrimSpace(notice)
	}
	if decision == nil {
		// Rust #47185: a failed fork keeps the locked view and its draft intact.
		m.readOnlyDraft = msg.retained
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return nil
	}
	// Rust #47185: the fork is editable, so the locked notice goes away and the
	// local draft continues in the new conversation.
	m.setReadOnlyThread(false)
	m.restoreComposerInputState(msg.retained, true)
	m.movePendingSteersToFork(msg.sourceThreadID, decision.Value, msg.retainedSteers)
	m.notice = "Fork created. You can continue here."
	m.refreshTranscript()
	if m.onModalResponse != nil {
		return m.onModalResponse(ModalResponse{
			ID:       "fork-current-session",
			Kind:     ModalKindPicker,
			OptionID: msg.selection.Target.ThreadID,
			Picker:   decision,
		})
	}
	return nil
}

// applyForkSummary applies a completed thread/fork response: naming, picker
// bookkeeping, the thread switch and the task-tool capability inheritance
// shared by the synchronous (unlocked) and asynchronous (locked) fork paths.
func (m *Model) applyForkSummary(selection codextui.SessionSelection, summary *codextui.SessionSummary) (*PickerDecision, string, bool) {
	threadID := strings.TrimSpace(selection.Target.ThreadID)
	decision := &PickerDecision{Kind: string(selection.Kind), Value: threadID}
	if summary != nil && strings.TrimSpace(summary.ThreadID) != "" {
		m.invalidateAppsScope()
		if name := strings.TrimSpace(selection.Name); name != "" {
			if m.onRenameThread == nil {
				m.addErrorHistoryMessage("Failed to name the forked session: rename runtime is unavailable")
			} else if err := m.onRenameThread(summary.ThreadID, name); err != nil {
				m.addErrorHistoryMessage("Failed to name the forked session: " + strings.TrimSpace(err.Error()))
			} else {
				summary.Title = name
			}
		}
		m.upsertSessionItem(*summary)
		m.deactivateSideConversation()
		m.switchThreadID(summary.ThreadID)
		m.inheritTaskToolCapability(threadID, summary.ThreadID)
		decision.Value = summary.ThreadID
		return decision, "Forked session " + summary.ThreadID, true
	}
	return decision, "Fork selected: " + selection.Target.DisplayLabel(), true
}

func (m *Model) applySessionSelection(selection codextui.SessionSelection) (*PickerDecision, string, bool) {
	threadID := strings.TrimSpace(selection.Target.ThreadID)
	decision := &PickerDecision{Kind: string(selection.Kind), Value: threadID}
	currentThreadID := ""
	if m != nil && m.State != nil {
		currentThreadID = strings.TrimSpace(m.State.ThreadID)
	}
	if threadID != "" && threadID != currentThreadID && m.rejectPendingPermissionRootSwitch("") {
		return nil, "", true
	}
	switch selection.Kind {
	case codextui.SessionSelectionResume:
		if threadID == "" {
			return decision, "Resume failed: missing thread id", true
		}
		// Rust #58457: a selection owned by another backend is resumed through
		// that owner; only a same-backend selection takes the local path.
		owner := m.selectionBackend(selection.Target)
		if !owner.IsZero() && owner != m.backendID {
			return m.applyBackendSwitchSelection(selection, owner, decision)
		}
		if !owner.IsZero() {
			// Recorded ownership is authoritative over a stale listing stamp, so
			// the resume handler accepts the thread it already serves.
			selection.Target.BackendID = owner
		}
		if m.onResumeSession != nil {
			resolved, needsPrompt, resolveNotice := m.resolveResumeSelectionCWD(selection)
			if strings.TrimSpace(resolveNotice) != "" {
				return nil, resolveNotice, true
			}
			if needsPrompt {
				m.openResumeCWDPromptForSelection(resolved)
				return nil, "", false
			}
			selection = resolved
			response, err := m.onResumeSession(selection)
			if err != nil {
				// Rust session_start.rs: an archived conversation is offered for
				// unarchiving and retried instead of failing the resume.
				if _, ok := archivedSessionGuidanceForThread(err, threadID); ok && m.openUnarchivePrompt(threadID, "resume", selection) {
					return nil, "", false
				}
				message := strings.TrimSpace(err.Error())
				if message == "" {
					message = "unknown error"
				}
				return nil, "Resume failed: " + message, true
			}
			if response.Summary != nil {
				m.upsertSessionItem(*response.Summary)
				if strings.TrimSpace(response.Summary.ThreadID) != "" {
					decision.Value = strings.TrimSpace(response.Summary.ThreadID)
				}
			}
			resolvedCWD := selection.ResumeCWD
			// A session-directory choice should prefer the handler's freshly
			// read summary over a possibly stale picker row. An explicit
			// Current choice stays authoritative.
			if response.Summary != nil &&
				strings.TrimSpace(selection.Target.CWD) != "" &&
				sameResumeCWD(selection.ResumeCWD, selection.Target.CWD) {
				if summaryCWD := strings.TrimSpace(response.Summary.CWD); summaryCWD != "" {
					resolvedCWD = summaryCWD
				}
			}
			m.applyResumeResponseWithResolvedCWD(threadID, response, resolvedCWD)
		} else if m.State != nil {
			m.deactivateSideConversation()
			m.switchThreadID(threadID)
			m.State.Messages = nil
			m.State.BumpMessagesRevision()
			m.insertSessionHeader()
			m.setStatus("idle")
			m.refreshTranscript()
			m.transcript.GotoBottom()
		}
		return decision, strings.TrimSpace(m.State.RenderSetting("Thread", firstNonEmpty(decision.Value, threadID))), true
	case codextui.SessionSelectionFork:
		summary, err := m.runSessionAction(selection)
		if err != nil {
			// Rust session_start.rs: forking an archived conversation prompts too.
			if _, ok := archivedSessionGuidanceForThread(err, threadID); ok && m.openUnarchivePrompt(threadID, "fork", selection) {
				return nil, "", false
			}
			return nil, err.Error(), true
		}
		return m.applyForkSummary(selection, summary)
	case codextui.SessionSelectionArchive:
		if _, err := m.runSessionAction(selection); err != nil {
			return nil, err.Error(), true
		}
		m.setSessionArchived(threadID, true)
		return decision, "Archived session " + threadID, true
	case codextui.SessionSelectionUnarchive:
		summary, err := m.runSessionAction(selection)
		if err != nil {
			return nil, err.Error(), true
		}
		if summary != nil {
			summary.Archived = false
			m.upsertSessionItem(*summary)
		} else {
			m.setSessionArchived(threadID, false)
		}
		return decision, "Unarchived session " + threadID, true
	case codextui.SessionSelectionDelete:
		if _, err := m.runSessionAction(selection); err != nil {
			return nil, err.Error(), true
		}
		m.removeSessionItem(threadID)
		if m.State.ThreadID == threadID {
			m.invalidateAppsScope()
			m.State.ResetThread()
		}
		return decision, "Deleted session " + threadID, true
	default:
		return decision, "", true
	}
}

func (m *Model) applyResumeResponse(threadID string, response SessionResumeResponse) {
	if m == nil {
		return
	}
	sessionCWD := ""
	if response.Summary != nil {
		sessionCWD = strings.TrimSpace(response.Summary.CWD)
	}
	currentCWD := strings.TrimSpace(m.sessionCWD)
	switch normalizeResumeCWD(m.resumeCWD) {
	case "current":
		if currentCWD == "" {
			currentCWD = sessionCWD
		}
		m.applyResumeResponseWithCWD(threadID, response, currentCWD)
	case "session":
		m.applyResumeResponseWithCWD(threadID, response, sessionCWD)
	default:
		if sessionCWD != "" && currentCWD != "" && !sameResumeCWD(currentCWD, sessionCWD) {
			m.openResumeCWDPrompt(threadID, response, currentCWD, sessionCWD)
			return
		}
		m.applyResumeResponseWithCWD(threadID, response, sessionCWD)
	}
}

// applyResumeResponseWithResolvedCWD applies a resume whose working directory
// was already resolved before the request. The legacy post-response prompt is
// reserved for responses that arrive without a selection-time cwd.
func (m *Model) applyResumeResponseWithResolvedCWD(threadID string, response SessionResumeResponse, cwd string) {
	if m == nil {
		return
	}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		m.applyResumeResponseWithCWD(threadID, response, cwd)
		return
	}
	m.applyResumeResponse(threadID, response)
}

func (m *Model) applyResumeResponseWithCWD(threadID string, response SessionResumeResponse, cwd string) {
	if m == nil || m.State == nil {
		return
	}
	m.releasePrimarySelectionOwnership()
	previousThreadID := m.currentThreadID()
	m.invalidateAppsScope()
	if response.Summary != nil && strings.TrimSpace(response.Summary.ThreadID) != "" {
		threadID = strings.TrimSpace(response.Summary.ThreadID)
	}
	m.resetAgentPickerRefresh(true)
	// Rust #43994: the resumed thread's replay must not inherit transcript
	// state from the thread that was open before.
	m.resetThreadScopedState()
	m.switchThreadID(threadID)
	m.seedRecapProgress(response.CompletedTurns)
	if response.Summary != nil {
		m.State.SetThreadName(response.Summary.Title)
		if cwd = strings.TrimSpace(cwd); cwd != "" {
			m.State.CWD = cwd
			m.sessionCWD = cwd
		}
		// Rust #43360: restore the thread's server-reported model and provider
		// so the resumed conversation keeps running on its own model.
		if model := strings.TrimSpace(response.Summary.Model); model != "" {
			m.State.Model = model
		}
		if provider := strings.TrimSpace(response.Summary.Provider); provider != "" {
			m.State.Provider = provider
		}
	}
	// Rust session_flow.rs / settings.rs (#49861): the resumed thread's
	// persisted Daybreak preference is the live value, gated by the
	// cli_daybreak feature. A summary without a preference clears it so the
	// next thread cannot inherit the previous one's Daybreak state.
	m.daybreakEnabled = false
	if response.Summary != nil && response.Summary.DaybreakEnabled {
		m.daybreakEnabled = m.daybreakFeatureEnabled()
	}
	// Rust #43253/#43330: the resume response carries the thread's server-owned
	// settings, and a read-only fallback replaces the composer with a notice.
	if response.ThreadSettings != nil {
		m.applyThreadSettingsValues(*response.ThreadSettings)
	}
	m.transcriptHistoryPartial = response.HasOlderHistory
	m.setReadOnlyThread(response.ReadOnly)
	if response.ProviderError != nil {
		// Rust #58202: the chat stays readable but read-only, and the composer
		// is replaced by the provider recovery notice.
		rolloutPath := ""
		if response.Summary != nil {
			rolloutPath = response.Summary.Path
		}
		m.showProviderError(response.ProviderError, rolloutPath)
	} else {
		m.clearProviderError()
	}
	m.setThreadUnavailable(false)
	m.State.Messages = append([]codextui.Message(nil), response.Messages...)
	m.State.BumpMessagesRevision()
	m.insertSessionHeader()
	// Rust startup.rs / session_lifecycle.rs #45980: a summary-only read-only
	// snapshot tells the user that intermediate items are unavailable.
	if notice := strings.TrimSpace(response.HistoryNotice); notice != "" {
		m.addInfoHistoryMessage(notice)
	}
	if previousThreadID != threadID {
		m.prependDaybreakNewTurnNotice()
	}
	if response.TokenUsage != nil {
		m.applyTokenUsage(response.TokenUsage)
	}
	status := strings.TrimSpace(response.Status)
	if status == "" {
		status = "idle"
	}
	m.setStatus(status)
	// Rust #54047: a replayed in-progress turn keeps the elapsed time it had
	// already spent instead of resetting the Working timer to zero.
	m.resetTaskTimerFromReplayedStart(response.WorkingTurnStartedAtMS)
	// Rust #43921: restore the resumed turn's active reasoning heading.
	m.resetReasoningSummaryHeader()
	m.setWorkingStatusHeader(response.WorkingStatusHeader)
	m.reasoningResumeTurnID = strings.TrimSpace(response.WorkingReasoningTurnID)
	m.reasoningItemID = strings.TrimSpace(response.WorkingReasoningItemID)
	m.applyRestoredReasoningHeading()
	// A restored active item may have missed earlier deltas; the completed item
	// reconciles the stream (Rust #43921 restore_active_reasoning_item).
	m.reasoningRecoveredAfterRefresh = strings.TrimSpace(response.WorkingReasoningItemID) != "" ||
		strings.TrimSpace(response.WorkingStatusHeader) != ""
	m.activeAgentLabel = ""
	if m.statusControls != nil {
		m.statusControls.SetActiveAgentLabel("", false)
	}
	// Rust #54387 (chatwidget/input_restore.rs:601,621): the thread's restored
	// input state carries the session-only status-surface overrides, so an
	// in-process restore re-applies what the user selected with /title and
	// /statusline instead of falling back to the saved layout.
	m.restoreStatusSurfaceOverrides(threadID)
	// The initial empty thread is a fresh task even when it arrives through
	// the session-configured/resume path; resumed threads keep their replay.
	if previousThreadID == "" && len(response.Messages) == 0 {
		m.markFreshAstraSparkle()
	}
	m.refreshTranscript()
	m.transcript.GotoBottom()
}

func normalizeSessionPickerDensityTea(value string) codextui.SessionListDensity {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "comfortable":
		return codextui.SessionDensityComfortable
	default:
		return codextui.SessionDensityDense
	}
}

func (m *Model) runSessionAction(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
	if m == nil || m.onSessionAction == nil {
		return nil, nil
	}
	return m.onSessionAction(selection)
}

func (m *Model) upsertSessionItem(item codextui.SessionSummary) {
	if m == nil || strings.TrimSpace(item.ThreadID) == "" {
		return
	}
	for i := range m.sessionItems {
		if m.sessionItems[i].ThreadID == item.ThreadID {
			m.sessionItems[i] = item
			return
		}
	}
	m.sessionItems = append([]codextui.SessionSummary{item}, m.sessionItems...)
}

func (m *Model) setSessionArchived(threadID string, archived bool) {
	if m == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	m.setAgentPickerThreadVisibility(threadID, archived)
	for i := range m.sessionItems {
		if m.sessionItems[i].ThreadID == threadID {
			m.sessionItems[i].Archived = archived
			return
		}
	}
}

func (m *Model) removeSessionItem(threadID string) {
	if m == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	items := m.sessionItems[:0]
	for _, item := range m.sessionItems {
		if item.ThreadID != threadID {
			items = append(items, item)
		}
	}
	m.sessionItems = items
}

func sessionPickerLabel(item codextui.SessionSummary) string {
	label := strings.TrimSpace(item.DisplayTitle())
	if label == "" {
		label = firstNonEmpty(item.ThreadID, item.Path, "Untitled session")
	}
	if strings.TrimSpace(item.ThreadID) != "" {
		label += " (" + strings.TrimSpace(item.ThreadID) + ")"
	}
	return label
}

func sessionPickerDescription(item codextui.SessionSummary) string {
	parts := []string{}
	if strings.TrimSpace(item.CWD) != "" {
		parts = append(parts, "cwd: "+strings.TrimSpace(item.CWD))
	}
	if strings.TrimSpace(item.Branch) != "" {
		parts = append(parts, "branch: "+strings.TrimSpace(item.Branch))
	}
	if strings.TrimSpace(item.Provider) != "" {
		parts = append(parts, "provider: "+strings.TrimSpace(item.Provider))
	}
	if item.Archived {
		parts = append(parts, "archived")
	}
	if strings.TrimSpace(item.ThreadID) == "" && strings.TrimSpace(item.Path) == "" {
		parts = append(parts, "updated: "+item.UpdatedAt.Format("2006-01-02"))
	}
	return strings.Join(parts, "  ")
}

func titleCaseASCII(value string) string {
	if value == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(value)
	if r == utf8.RuneError && size == 0 {
		return ""
	}
	return string(unicode.ToUpper(r)) + value[size:]
}
