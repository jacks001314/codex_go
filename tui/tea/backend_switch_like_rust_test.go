package tea

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// backendSwitchModel is the Rust #58457 fixture: a TUI displaying a thread of
// one backend with a composer draft, plus the host hooks used to prepare a
// conversation owned by another backend.
func backendSwitchModel(t *testing.T, from codextui.BackendID, hook BackendSwitchFunc, resume SessionResumeFunc) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.Messages = []codextui.Message{{Role: codextui.RoleUser, Text: "source history"}}
	model := NewModel(state, Options{
		Width:                  100,
		Height:                 30,
		BackendID:              from,
		ResumeCWD:              "session",
		SessionPickerCWD:       "/source",
		OnResumeSession:        resume,
		OnSwitchBackendSession: hook,
	})
	model.switchThreadID("thread-source")
	model.composer.SetValue("keep this draft")
	return model
}

func backendSwitchResponse() SessionResumeResponse {
	return SessionResumeResponse{
		Summary: &codextui.SessionSummary{
			ThreadID: "thread-dest",
			Title:    "Destination",
			CWD:      "/dest",
		},
		Messages: []codextui.Message{{Role: codextui.RoleAssistant, Text: "destination history"}},
		Status:   "idle",
	}
}

func backendSwitchSelection(owner codextui.BackendID) codextui.SessionSelection {
	return codextui.SessionSelection{
		Kind: codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{
			BackendID: owner,
			ThreadID:  "thread-dest",
			CWD:       "/dest",
		},
	}
}

// TestCrossBackendResumeAdoptsOwningBackendLikeRust locks Rust #58457: selecting
// a conversation listed by another backend prepares it through that owner and
// only then replaces the displayed conversation.
func TestCrossBackendResumeAdoptsOwningBackendLikeRust(t *testing.T) {
	from := codextui.NewBackendID()
	owner := codextui.NewBackendID()
	var request BackendSwitchRequest
	resumeCalls := 0
	hook := func(got BackendSwitchRequest) (BackendSwitchResult, error) {
		request = got
		return BackendSwitchResult{Response: backendSwitchResponse()}, nil
	}
	model := backendSwitchModel(t, from, hook, func(codextui.SessionSelection) (SessionResumeResponse, error) {
		resumeCalls++
		return SessionResumeResponse{}, errors.New("same-backend resume must not run")
	})

	decision, notice, complete := model.applySessionSelection(backendSwitchSelection(owner))
	if decision == nil || !complete || !strings.Contains(notice, "thread-dest") {
		t.Fatalf("decision=%#v notice=%q complete=%v", decision, notice, complete)
	}
	if resumeCalls != 0 {
		t.Fatalf("same-backend resume ran %d times for a foreign selection", resumeCalls)
	}
	if request.FromBackendID != from || request.FromThreadID != "thread-source" {
		t.Fatalf("request from = %v/%q", request.FromBackendID, request.FromThreadID)
	}
	if request.Target.BackendID != owner || request.Target.ThreadID != "thread-dest" {
		t.Fatalf("request target = %v/%q", request.Target.BackendID, request.Target.ThreadID)
	}
	if model.backendID != owner {
		t.Fatalf("displayed backend = %v, want %v", model.backendID, owner)
	}
	if recorded, ok := model.threadBackendOwner("thread-dest"); !ok || recorded != owner {
		t.Fatalf("recorded owner = %v/%v", recorded, ok)
	}
	if model.State.ThreadID != "thread-dest" {
		t.Fatalf("displayed thread = %q", model.State.ThreadID)
	}
	if len(model.State.Messages) != 1 || model.State.Messages[0].Text != "destination history" {
		t.Fatalf("transcript = %#v", model.State.Messages)
	}
	if decision.Value != "thread-dest" {
		t.Fatalf("decision value = %q", decision.Value)
	}
	if got := model.composerText(); got != "keep this draft" {
		t.Fatalf("composer draft = %q", got)
	}
}

// TestBackendSwitchPreparationFailureKeepsCurrentSessionLikeRust locks Rust's
// handoff contract: a destination that cannot be prepared reports the failure
// and leaves the current thread, transcript and draft untouched.
func TestBackendSwitchPreparationFailureKeepsCurrentSessionLikeRust(t *testing.T) {
	from := codextui.NewBackendID()
	owner := codextui.NewBackendID()
	model := backendSwitchModel(t, from, func(BackendSwitchRequest) (BackendSwitchResult, error) {
		return BackendSwitchResult{}, errors.New("config/read failed while checking remote project trust")
	}, nil)

	decision, notice, complete := model.applySessionSelection(backendSwitchSelection(owner))
	const want = "Failed to prepare session backend: config/read failed while checking remote project trust"
	if decision != nil || !complete || notice != want {
		t.Fatalf("decision=%#v notice=%q complete=%v want %q", decision, notice, complete, want)
	}
	if model.backendID != from {
		t.Fatalf("displayed backend = %v, want %v", model.backendID, from)
	}
	if model.State.ThreadID != "thread-source" {
		t.Fatalf("displayed thread = %q", model.State.ThreadID)
	}
	// Rust add_session_picker_error appends the refusal; the conversation stays.
	if len(model.State.Messages) != 2 || model.State.Messages[0].Text != "source history" {
		t.Fatalf("transcript = %#v", model.State.Messages)
	}
	if last := model.State.Messages[1].Text; !strings.Contains(last, want) {
		t.Fatalf("transcript error = %q", last)
	}
	if got := model.composerText(); got != "keep this draft" {
		t.Fatalf("composer draft = %q", got)
	}
	if _, ok := model.threadBackendOwner("thread-dest"); ok {
		t.Fatal("failed handoff recorded an ownership for the destination")
	}
}

// TestBackendSwitchBlockedDuringActiveTurnLikeRust locks Rust's refusal while
// the displayed thread still runs a turn.
func TestBackendSwitchBlockedDuringActiveTurnLikeRust(t *testing.T) {
	from := codextui.NewBackendID()
	owner := codextui.NewBackendID()
	calls := 0
	model := backendSwitchModel(t, from, func(BackendSwitchRequest) (BackendSwitchResult, error) {
		calls++
		return BackendSwitchResult{Response: backendSwitchResponse()}, nil
	}, nil)
	model.State.Status = "running"

	decision, notice, complete := model.applySessionSelection(backendSwitchSelection(owner))
	if decision != nil || !complete || notice != "Wait for the current turn to finish before switching backends." {
		t.Fatalf("decision=%#v notice=%q complete=%v", decision, notice, complete)
	}
	if calls != 0 {
		t.Fatalf("prepared %d destinations during an active turn", calls)
	}
	if model.State.ThreadID != "thread-source" || model.backendID != from {
		t.Fatalf("switch mutated state: thread=%q backend=%v", model.State.ThreadID, model.backendID)
	}
}

// TestBackendSwitchWithoutHostIsUnavailableLikeRust locks the message shown when
// no host can reach the owning backend.
func TestBackendSwitchWithoutHostIsUnavailableLikeRust(t *testing.T) {
	from := codextui.NewBackendID()
	model := backendSwitchModel(t, from, nil, nil)
	decision, notice, complete := model.applySessionSelection(backendSwitchSelection(codextui.NewBackendID()))
	if decision != nil || !complete || notice != "This session's backend is unavailable. Open the session picker again." {
		t.Fatalf("decision=%#v notice=%q complete=%v", decision, notice, complete)
	}
	if model.State.ThreadID != "thread-source" {
		t.Fatalf("displayed thread = %q", model.State.ThreadID)
	}
}

// TestRecordedOwnershipWinsOverStalePickerStampLikeRust locks Rust #58457 thread
// ownership tracking: once a thread was adopted from another backend, a picker
// row that still carries the old listing stamp keeps the adopted backend.
func TestRecordedOwnershipWinsOverStalePickerStampLikeRust(t *testing.T) {
	from := codextui.NewBackendID()
	owner := codextui.NewBackendID()
	model := backendSwitchModel(t, from, func(BackendSwitchRequest) (BackendSwitchResult, error) {
		return BackendSwitchResult{Response: backendSwitchResponse()}, nil
	}, nil)
	if decision, notice, _ := model.applySessionSelection(backendSwitchSelection(owner)); decision == nil {
		t.Fatalf("initial switch notice = %q", notice)
	}

	var resumed codextui.SessionSelection
	model.onResumeSession = func(selection codextui.SessionSelection) (SessionResumeResponse, error) {
		resumed = selection
		return backendSwitchResponse(), nil
	}
	model.onSwitchBackendSession = func(BackendSwitchRequest) (BackendSwitchResult, error) {
		t.Fatal("a recorded thread was switched back to its stale listing backend")
		return BackendSwitchResult{}, nil
	}
	if decision, notice, _ := model.applySessionSelection(backendSwitchSelection(from)); decision == nil {
		t.Fatalf("stale selection notice = %q", notice)
	}
	if resumed.Target.BackendID != owner {
		t.Fatalf("resume backend = %v, want the recorded owner %v", resumed.Target.BackendID, owner)
	}
	if model.backendID != owner {
		t.Fatalf("displayed backend = %v, want %v", model.backendID, owner)
	}
}

// TestBackgroundBackendRequestWaitsForDisplayLikeRust locks Rust #58457
// pending_requests: a request addressed to a backend that is not displayed is
// parked and replays once that backend is shown, and a resolved request never
// replays.
func TestBackgroundBackendRequestWaitsForDisplayLikeRust(t *testing.T) {
	from := codextui.NewBackendID()
	owner := codextui.NewBackendID()
	model := backendSwitchModel(t, from, nil, nil)
	model.noteThreadBackend("thread-background", owner)

	if cmd := model.routeApprovalRequest(ApprovalRequestMsg{ID: "approval-1", ServerRequestID: "request-1", ThreadID: "thread-background"}); cmd != nil {
		t.Fatal("a background backend request produced a command")
	}
	if model.modal != nil || len(model.pendingApprovals) != 0 {
		t.Fatalf("background request surfaced early: modal=%#v queued=%d", model.modal, len(model.pendingApprovals))
	}
	if len(model.pendingBackendRequests) != 1 {
		t.Fatalf("parked requests = %d, want 1", len(model.pendingBackendRequests))
	}

	model.backendID = owner
	model.switchThreadID("thread-background")
	model.flushBackendRequests()
	if len(model.pendingBackendRequests) != 0 {
		t.Fatalf("parked requests after display = %d", len(model.pendingBackendRequests))
	}
	if model.modal == nil || model.modal.kind != ModalKindApproval {
		t.Fatalf("replayed request modal = %#v", model.modal)
	}

	resolved := backendSwitchModel(t, from, nil, nil)
	resolved.noteThreadBackend("thread-background", owner)
	resolved.routeApprovalRequest(ApprovalRequestMsg{ID: "approval-2", ServerRequestID: "request-2", ThreadID: "thread-background"})
	resolved.applyServerRequestResolved(ServerRequestResolvedMsg{ThreadID: "thread-background", RequestID: "request-2"})
	if len(resolved.pendingBackendRequests) != 0 {
		t.Fatalf("resolved request stayed parked: %d", len(resolved.pendingBackendRequests))
	}
}
