package tea

import (
	"errors"
	"strings"

	codextui "codex_go/tui"
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

// noteThreadBackend records the backend that serves a displayed thread (Rust
// #58457 thread ownership tracking). Ownership outlives the picker row that
// listed the thread, so a later selection keeps the backend that adopted it.
func (m *Model) noteThreadBackend(threadID string, owner codextui.BackendID) {
	if m == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || owner.IsZero() {
		return
	}
	if m.threadBackend == nil {
		m.threadBackend = map[string]codextui.BackendID{}
	}
	m.threadBackend[threadID] = owner
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
	resolved, needsPrompt, resolveNotice := m.resolveResumeSelectionCWD(selection)
	if strings.TrimSpace(resolveNotice) != "" {
		return nil, resolveNotice, true
	}
	if needsPrompt {
		m.openResumeCWDPromptForSelection(resolved)
		return nil, "", false
	}
	resolved.Target.BackendID = owner
	result, err := m.onSwitchBackendSession(BackendSwitchRequest{
		FromBackendID: m.backendID,
		FromThreadID:  strings.TrimSpace(m.currentThreadID()),
		Target:        resolved.Target,
		ResumeCWD:     resolved.ResumeCWD,
	})
	if err != nil {
		detail := strings.TrimSpace(err.Error())
		if detail == "" {
			detail = "unknown error"
		}
		return m.failBackendSwitch(backendSwitchPrepareFailedPrefix + detail)
	}
	next := owner
	if result.Response.Summary != nil && !result.Response.Summary.BackendID.IsZero() {
		next = result.Response.Summary.BackendID
	}
	if m.backendID != next {
		// Adopt the owner before the replay installs the thread, so
		// switchThreadID records the resumed conversation under the backend
		// that now serves it (Rust backends.displayed = destination_slot).
		m.backendID = next
	}
	if decision == nil {
		decision = &PickerDecision{Kind: string(selection.Kind), Value: threadID}
	}
	resolvedCWD := resolved.ResumeCWD
	if result.Response.Summary != nil {
		m.upsertSessionItem(*result.Response.Summary)
		if id := strings.TrimSpace(result.Response.Summary.ThreadID); id != "" {
			decision.Value = id
		}
		// A session-directory choice prefers the handler's freshly read summary
		// over the possibly stale picker row (same rule as a same-backend resume).
		if strings.TrimSpace(selection.Target.CWD) != "" && sameResumeCWD(resolved.ResumeCWD, selection.Target.CWD) {
			if summaryCWD := strings.TrimSpace(result.Response.Summary.CWD); summaryCWD != "" {
				resolvedCWD = summaryCWD
			}
		}
	}
	m.applyResumeResponseWithResolvedCWD(threadID, result.Response, resolvedCWD)
	m.flushBackendRequests()
	return decision, strings.TrimSpace(m.State.RenderSetting("Thread", firstNonEmpty(decision.Value, threadID))), true
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
