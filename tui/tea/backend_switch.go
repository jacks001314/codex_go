package tea

import (
	"errors"
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	"codex_go/tui/mousemsg"
)

// Rust #58457 (tui/src/app/backend_selection.rs resume_backend_session): the
// session picker can list conversations owned by another backend. Selecting one
// prepares the destination backend first and only then replaces the displayed
// conversation, so a failed handoff keeps the current thread, transcript and
// composer draft intact.
//
// Go's TUI owns a single connection at a time, so the owner is resolved by a
// host hook (Options.OnSwitchBackendSession) instead of Rust's registered
// BackendSessions collection.

const (
	// backendSwitchUnavailableMessage is Rust's message when the owning backend
	// has no connection this runtime can reach.
	backendSwitchUnavailableMessage = "This session's backend is unavailable. Open the session picker again."
	// backendSwitchActiveTurnMessage refuses a switch while the displayed
	// thread still runs a turn.
	backendSwitchActiveTurnMessage = "Wait for the current turn to finish before switching backends."
	// backendSwitchPrepareFailedPrefix prefixes a destination preparation error
	// (Rust `format!("Failed to prepare session backend: {error}")`).
	backendSwitchPrepareFailedPrefix = "Failed to prepare session backend: "
)

// BackendSwitchRequest describes a picker resume owned by another backend.
type BackendSwitchRequest struct {
	// FromBackendID is the backend whose conversation is displayed now; the
	// host detaches the outgoing thread through that connection.
	FromBackendID codextui.BackendID
	// FromThreadID is the conversation the user is leaving.
	FromThreadID string
	// Target is the picker selection. Target.BackendID is the owning backend.
	Target codextui.SessionTarget
	// ResumeCWD is the working directory already resolved by the
	// `tui.resume_cwd` policy, so the owner prepares the right directory.
	ResumeCWD string
}

// BackendSwitchResult carries the prepared conversation. A host returns it only
// after the owning backend accepted the resume; the model then swaps the
// displayed conversation.
type BackendSwitchResult struct {
	Response SessionResumeResponse
}

// backendSwitchResultMsg delivers the prepared conversation back to the update
// loop. Rust #61481 runs the Cloud handoff as an awaited future while the TUI
// keeps handling draft input, so the Go handoff leaves the update loop through
// this message instead of blocking it.
type backendSwitchResultMsg struct {
	id     uint64
	result BackendSwitchResult
	err    error
}

// pendingBackendSwitch is one cross-backend preparation owned by the model.
// The request fields are captured so the Cmd goroutine never reads the model.
type pendingBackendSwitch struct {
	id             uint64
	request        BackendSwitchRequest
	selection      codextui.SessionSelection
	decision       *PickerDecision
	resolvedCWD    string
	responseID     string
	responseOption string
	// fromAgentsOverview distinguishes the command-center resume flow, which
	// captures and restores per-task drafts around the switch.
	fromAgentsOverview bool
	switching          bool
}

// ErrBackendSwitchUnavailable reports that the host cannot reach the owning
// backend, so the selection must not be attempted.
var ErrBackendSwitchUnavailable = errors.New("backend unavailable")

// pendingBackendRequest is one server request parked for a backend that is not
// displayed (Rust BackendSessions::pending_requests).
type pendingBackendRequest struct {
	BackendID codextui.BackendID
	Approval  *ApprovalRequestMsg
	Modal     *ModalRequestMsg
}

// initialCloudBackends seeds the Cloud identity of the launch backend. Rust
// #61742 collects cloud backends from every app-server session at startup and
// keeps that set separate from the live connections.
func initialCloudBackends(options Options) map[codextui.BackendID]bool {
	backends := map[codextui.BackendID]bool{}
	if options.ServerOwnedSettings && !options.BackendID.IsZero() {
		backends[options.BackendID] = true
	}
	return backends
}

// isCloudBackend reports whether a logical backend hosts Cloud conversations.
func (m *Model) isCloudBackend(backendID codextui.BackendID) bool {
	if m == nil || backendID.IsZero() {
		return false
	}
	return m.cloudBackends[backendID]
}

// threadLabelCloud resolves a thread's backend identity for its display
// marker. Recorded ownership wins over the current backend, and the marker
// remains available after the connection disappears (Rust #61742).
func (m *Model) threadLabelCloud(threadID string, fallback codextui.BackendID) bool {
	if m == nil {
		return false
	}
	if owner, ok := m.threadBackendOwner(threadID); ok {
		return m.isCloudBackend(owner)
	}
	if fallback.IsZero() {
		fallback = m.backendID
	}
	return m.isCloudBackend(fallback)
}

// noteThreadBackend records the backend that serves a displayed thread (Rust
// #58457 thread ownership tracking). Ownership outlives the picker row that
// listed the thread, so a later selection keeps the backend that adopted it.
//
// Rust #60929 keeps the first observed backend: a conversation belongs to one
// backend even when a later listing or connection reports it elsewhere. A
// conflicting observation is ignored and reported so callers can refuse to
// route the thread through the wrong backend.
func (m *Model) noteThreadBackend(threadID string, owner codextui.BackendID) bool {
	if m == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || owner.IsZero() {
		return false
	}
	if m.threadBackend == nil {
		m.threadBackend = map[string]codextui.BackendID{}
	}
	if existing, ok := m.threadBackend[threadID]; ok && !existing.IsZero() {
		return existing == owner
	}
	m.threadBackend[threadID] = owner
	return true
}

// threadBackendOwner reports the backend a thread was last displayed through.
func (m *Model) threadBackendOwner(threadID string) (codextui.BackendID, bool) {
	if m == nil {
		return codextui.BackendID{}, false
	}
	owner, ok := m.threadBackend[strings.TrimSpace(threadID)]
	if !ok || owner.IsZero() {
		return codextui.BackendID{}, false
	}
	return owner, true
}

// selectionBackend reports the backend that owns a picker target. Recorded
// ownership wins over a possibly stale listing stamp; an unstamped target
// belongs to the displayed backend.
func (m *Model) selectionBackend(target codextui.SessionTarget) codextui.BackendID {
	if owner, ok := m.threadBackendOwner(target.ThreadID); ok {
		return owner
	}
	if !target.BackendID.IsZero() {
		return target.BackendID
	}
	if m == nil {
		return codextui.BackendID{}
	}
	return m.backendID
}

// applyBackendSwitchSelection resumes a selection owned by another backend.
// Nothing is mutated before the host has prepared the destination: a refusal or
// a preparation error leaves the thread, transcript and draft untouched.
func (m *Model) applyBackendSwitchSelection(selection codextui.SessionSelection, owner codextui.BackendID, decision *PickerDecision) (*PickerDecision, string, bool) {
	threadID := strings.TrimSpace(selection.Target.ThreadID)
	// Rust checks both guards before touching the destination backend.
	if m.rejectPendingPermissionRootSwitch("") {
		return nil, "", true
	}
	if m.isTaskRunning() {
		return m.failBackendSwitch(backendSwitchActiveTurnMessage)
	}
	if m.onSwitchBackendSession == nil {
		return m.failBackendSwitch(backendSwitchUnavailableMessage)
	}
	if m.pendingBackendSwitch != nil {
		// Rust keeps the pending handoff as the only navigation owner, so a
		// second selection waits instead of replacing its destination.
		return m.failBackendSwitch("Wait for the current session switch to finish.")
	}
	resolved, needsPrompt, resolveNotice := m.resolveResumeSelectionCWD(selection)
	if strings.TrimSpace(resolveNotice) != "" {
		return nil, resolveNotice, true
	}
	if needsPrompt {
		m.openResumeCWDPromptForSelection(resolved)
		return nil, "", false
	}
	resolved.Target.BackendID = owner
	state := &pendingBackendSwitch{
		id: m.nextBackendSwitchID(),
		request: BackendSwitchRequest{
			FromBackendID: m.backendID,
			FromThreadID:  strings.TrimSpace(m.currentThreadID()),
			Target:        resolved.Target,
			ResumeCWD:     resolved.ResumeCWD,
		},
		selection:          resolved,
		decision:           decision,
		resolvedCWD:        resolved.ResumeCWD,
		fromAgentsOverview: m.agentsOverview != nil,
		switching:          threadID != strings.TrimSpace(m.currentThreadID()),
	}
	if state.decision == nil {
		state.decision = &PickerDecision{Kind: string(selection.Kind), Value: threadID}
	}
	state.responseOption = firstNonEmpty(state.decision.Value, threadID)
	// A picker owns the accepted selection; Rust closes it before the handoff
	// paints its connecting state, and the modal response is delivered once the
	// destination has been prepared.
	if m.modal != nil {
		// The accepted selection owns the open surface; the handoff paints its
		// own progress and reports the modal response on adoption.
		state.responseID = m.modal.id
		m.modal = nil
	}
	m.pendingBackendSwitch = state
	m.backendSwitchCmd = m.beginBackendSwitchCommand(state)
	return nil, "", false
}

// beginBackendSwitchCommand runs the destination preparation on its own
// goroutine. The closure captures only immutable request values, so quitting or
// abandoning the switch cannot race with a torn-down model.
func (m *Model) beginBackendSwitchCommand(state *pendingBackendSwitch) bubbletea.Cmd {
	if m == nil || state == nil || m.onSwitchBackendSession == nil {
		return nil
	}
	hook := m.onSwitchBackendSession
	request := state.request
	id := state.id
	return func() bubbletea.Msg {
		result, err := hook(request)
		return backendSwitchResultMsg{id: id, result: result, err: err}
	}
}

func (m *Model) nextBackendSwitchID() uint64 {
	m.backendSwitchSeq++
	return m.backendSwitchSeq
}

// consumeBackendSwitchCmd hands the initial preparation command to the
// Bubble Tea program after the update that started it.
func (m *Model) consumeBackendSwitchCmd() bubbletea.Cmd {
	if m == nil || m.backendSwitchCmd == nil {
		return nil
	}
	cmd := m.backendSwitchCmd
	m.backendSwitchCmd = nil
	return cmd
}

// applyBackendSwitchResult adopts a prepared destination. A result whose
// generation no longer matches belongs to an abandoned handoff and is dropped
// without touching the displayed conversation.
func (m *Model) applyBackendSwitchResult(message backendSwitchResultMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	state := m.pendingBackendSwitch
	if state == nil || state.id != message.id {
		return nil
	}
	m.pendingBackendSwitch = nil
	if message.err != nil {
		detail := strings.TrimSpace(message.err.Error())
		if detail == "" {
			detail = "unknown error"
		}
		m.failBackendSwitch(backendSwitchPrepareFailedPrefix + detail)
		return nil
	}
	result := message.result
	next := state.selection.Target.BackendID
	if result.Response.Summary != nil && !result.Response.Summary.BackendID.IsZero() {
		next = result.Response.Summary.BackendID
	}
	if m.backendID != next {
		// Adopt the owner before the replay installs the thread, so
		// switchThreadID records the resumed conversation under the backend
		// that now serves it (Rust backends.displayed = destination_slot).
		m.backendID = next
	}
	if backendState := m.agentsOverviewBackendPeek(next); backendState != nil {
		// Preparing the destination succeeded, so it is reachable again.
		backendState.disconnected = false
	}
	// Rust #61596: pagination and failure state follow the destination backend
	// instead of being reset by the switch.
	m.syncAgentsOverviewBackendView()
	decision := state.decision
	if decision == nil {
		decision = &PickerDecision{Kind: string(state.selection.Kind), Value: strings.TrimSpace(state.selection.Target.ThreadID)}
	}
	resolvedCWD := state.resolvedCWD
	if result.Response.Summary != nil {
		m.upsertSessionItem(*result.Response.Summary)
		if id := strings.TrimSpace(result.Response.Summary.ThreadID); id != "" {
			decision.Value = id
		}
		// A session-directory choice prefers the handler's freshly read summary
		// over the possibly stale picker row (same rule as a same-backend resume).
		if strings.TrimSpace(state.selection.Target.CWD) != "" && sameResumeCWD(state.resolvedCWD, state.selection.Target.CWD) {
			if summaryCWD := strings.TrimSpace(result.Response.Summary.CWD); summaryCWD != "" {
				resolvedCWD = summaryCWD
			}
		}
	}
	threadID := strings.TrimSpace(state.selection.Target.ThreadID)
	m.applyResumeResponseWithResolvedCWD(threadID, result.Response, resolvedCWD)
	m.flushBackendRequests()
	if state.fromAgentsOverview && state.switching {
		// Rust #42419 keeps each task's composer draft across the command-center
		// resume; the asynchronous handoff must restore it when the destination
		// is finally adopted.
		m.restoreAgentsOverviewResumeDraft(strings.TrimSpace(decision.Value))
		m.maybeCloseAgentsOverviewAfterResume(strings.TrimSpace(m.currentThreadID()))
	}
	m.notice = strings.TrimSpace(m.State.RenderSetting("Thread", firstNonEmpty(decision.Value, threadID)))
	m.refreshTranscript()
	if m.onModalResponse == nil || strings.TrimSpace(state.responseID) == "" {
		return nil
	}
	optionID := state.responseOption
	if strings.TrimSpace(decision.Value) != "" {
		optionID = decision.Value
	}
	return m.onModalResponse(ModalResponse{
		ID:       state.responseID,
		Kind:     ModalKindPicker,
		OptionID: optionID,
		Picker:   decision,
	})
}

// updateBackendSwitchKey keeps the composer editable while a cross-backend
// handoff runs. Ctrl+C first clears the draft (Rust
// `cancel_cloud_handoff_input`), then quits when no input surface owns it;
// Ctrl+D quits only with an empty draft. Submission and app shortcuts wait for
// the handoff to finish (Rust RestrictedInputMode::DraftHandoff).
func (m *Model) updateBackendSwitchKey(msg keymsg.KeyMsg) (bubbletea.Cmd, bool) {
	if m == nil || m.pendingBackendSwitch == nil {
		return nil, false
	}
	// A pre-existing modal, overlay or alternate screen keeps its normal key
	// ownership; the handoff only edits the conversation composer.
	if m.modal != nil || m.overlay != nil || m.agentsOverview != nil ||
		(m.analyticsDashboard != nil && m.analyticsDashboard.active) || m.readOnlyThread {
		return nil, false
	}
	switch msg.Type {
	case keymsg.KeyCtrlC:
		m.activeKeyChord = ""
		m.pendingKeyChordPrefix = ""
		if m.cancelBackendSwitchDraftInput() {
			return nil, true
		}
		if m.backendSwitchNoPopupActive() {
			m.abandonBackendSwitch()
			return m.quitCmd(), true
		}
		return nil, true
	case keymsg.KeyCtrlD:
		if m.composerIsEmpty() && m.backendSwitchNoPopupActive() {
			m.abandonBackendSwitch()
			return m.quitCmd(), true
		}
		return nil, true
	}
	if m.warningsView != nil {
		return m.updateWarningsViewKey(msg), true
	}
	spec := keySpecFromKeyMsg(msg)
	if m.keyMatches("composer", "submit", spec) || m.keyMatches("composer", "queue", spec) {
		return nil, true
	}
	if isImagePasteKey(msg) {
		return m.pasteClipboardShortcut(), true
	}
	if msg.Type == keymsg.KeyRunes && msg.Paste {
		if pasted := string(msg.Runes); pasted != "" {
			m.insertComposerPaste(pasted)
			m.extendComposerPasteWindow(m.currentTime())
		}
		return nil, true
	}
	now := m.currentTime()
	if msg.Type == keymsg.KeyRunes {
		m.noteComposerRunes(msg.Runes, now)
	}
	if msg.Type == keymsg.KeyEnter {
		// Enter cannot submit during the handoff; only a legacy paste newline
		// survives, matching Rust's RestrictedInputMode::DraftHandoff.
		if m.shouldPasteBurstEnterInsertNewline(now) {
			m.extendComposerPasteWindow(now)
			return m.insertComposerNewline(), true
		}
		return nil, true
	}
	if msg.Type == keymsg.KeyTab {
		if m.shouldPasteBurstTabInsertInsteadOfShortcut(now) {
			m.insertComposerPasteTab()
			m.extendComposerPasteWindow(now)
		}
		return nil, true
	}
	composerBefore := m.composer.Value()
	if handled, prefixCmd := m.handleComposerPrefixKey(msg); handled {
		m.syncComposerElements(composerBefore)
		return prefixCmd, true
	}
	if handled, selectionCmd := m.applyComposerSelectionKey(msg); handled {
		m.syncComposerPrefixAfterEdit(composerBefore)
		m.syncComposerElements(composerBefore)
		return selectionCmd, true
	}
	var composerCmd bubbletea.Cmd
	m.composer, composerCmd = m.composer.Update(mousemsg.OriginalMessage(keymsg.OriginalMessage(msg)))
	m.syncComposerPrefixAfterEdit(composerBefore)
	m.syncComposerElements(composerBefore)
	return composerCmd, true
}

// cancelBackendSwitchDraftInput flushes buffered paste state into the draft and
// applies Ctrl+C's draft-cancellation step. It reports whether the draft owned
// the keystroke.
func (m *Model) cancelBackendSwitchDraftInput() bool {
	if m == nil {
		return false
	}
	// Go inserts pasted text immediately; settling the burst window is the
	// equivalent of Rust's flush_composer_paste_burst.
	m.composerPasteEnterUntil = nil
	if m.composerIsEmpty() {
		return false
	}
	m.resetComposer()
	m.refreshTranscript()
	return true
}

// backendSwitchNoPopupActive mirrors Rust's `no_modal_or_popup_active`: the
// prompt, completion popups, warnings view, feature questions and the quick
// dial menu all keep Ctrl+C for themselves.
func (m *Model) backendSwitchNoPopupActive() bool {
	if m == nil {
		return true
	}
	return m.modal == nil && m.overlay == nil && m.warningsView == nil &&
		!m.slashPopup.Active && !m.skillPopup.Active && m.mentionPopup == nil &&
		!m.asyncQuestions.Expanded() && !m.quickDialMenu &&
		(m.historySearch == nil || !m.historySearch.Active)
}

// abandonBackendSwitch forgets the pending handoff so its late result cannot
// replace the active attachment (Rust drops the abandoned Cloud start).
func (m *Model) abandonBackendSwitch() {
	if m == nil {
		return
	}
	m.pendingBackendSwitch = nil
	m.backendSwitchCmd = nil
}

// failBackendSwitch renders a refused handoff without touching the displayed
// conversation (Rust add_session_picker_error).
func (m *Model) failBackendSwitch(message string) (*PickerDecision, string, bool) {
	if m == nil {
		return nil, message, true
	}
	m.notice = message
	m.addErrorHistoryMessage(message)
	m.refreshTranscript()
	return nil, message, true
}

// requestBackendOwner resolves the backend a server request came from: an
// explicit stamp from a multi-connection host wins, otherwise the recorded
// ownership of its thread.
func (m *Model) requestBackendOwner(explicit codextui.BackendID, threadID string) (codextui.BackendID, bool) {
	if !explicit.IsZero() {
		return explicit, true
	}
	return m.threadBackendOwner(threadID)
}

// deferBackendRequest parks a request addressed to a backend that is not
// displayed. It reports whether the request was parked.
func (m *Model) deferBackendRequest(backendID codextui.BackendID, approval *ApprovalRequestMsg, modal *ModalRequestMsg) bool {
	if m == nil || backendID.IsZero() || backendID == m.backendID {
		return false
	}
	m.pendingBackendRequests = append(m.pendingBackendRequests, pendingBackendRequest{
		BackendID: backendID,
		Approval:  approval,
		Modal:     modal,
	})
	return true
}

// flushBackendRequests replays what was parked for the displayed backend.
func (m *Model) flushBackendRequests() {
	if m == nil || len(m.pendingBackendRequests) == 0 {
		return
	}
	var replay []pendingBackendRequest
	kept := m.pendingBackendRequests[:0]
	for _, pending := range m.pendingBackendRequests {
		if pending.BackendID == m.backendID {
			replay = append(replay, pending)
			continue
		}
		kept = append(kept, pending)
	}
	m.pendingBackendRequests = kept
	for _, pending := range replay {
		switch {
		case pending.Approval != nil:
			m.routeApprovalRequest(*pending.Approval)
		case pending.Modal != nil && m.modal == nil:
			m.openModal(*pending.Modal)
		case pending.Modal != nil:
			m.pendingBackendRequests = append(m.pendingBackendRequests, pending)
		}
	}
}

// dropBackendRequest forgets parked requests the server resolved elsewhere
// (Rust BackendSessions::next_event ServerRequestResolved cleanup).
func (m *Model) dropBackendRequest(requestID string) {
	if m == nil || len(m.pendingBackendRequests) == 0 {
		return
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}
	kept := m.pendingBackendRequests[:0]
	for _, pending := range m.pendingBackendRequests {
		if pending.Approval != nil && matchesBackendRequest(pending.Approval.ServerRequestID, pending.Approval.ID, requestID) {
			continue
		}
		if pending.Modal != nil && matchesBackendRequest(pending.Modal.ServerRequestID, pending.Modal.ID, requestID) {
			continue
		}
		kept = append(kept, pending)
	}
	m.pendingBackendRequests = kept
}

// dropStaleBackendRequests forgets everything parked when the connection goes
// away: those request brokers belong to the retired client.
func (m *Model) dropStaleBackendRequests() {
	if m == nil {
		return
	}
	m.pendingBackendRequests = nil
}

func matchesBackendRequest(serverRequestID string, id string, requestID string) bool {
	if strings.TrimSpace(serverRequestID) == requestID {
		return true
	}
	return strings.TrimSpace(id) == requestID
}
