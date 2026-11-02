package app

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// TestInteractiveBackendSwitchPreparesThroughOwningConnectionLikeRust mirrors
// Rust #58457 (app/backend_selection.rs resume_backend_session): a target listed
// by another backend is prepared with the connection this runtime holds, and
// the prepared summary keeps the owning backend so the TUI tracks the thread's
// real owner.
func TestInteractiveBackendSwitchPreparesThroughOwningConnectionLikeRust(t *testing.T) {
	local := codextui.ProcessBackendID()
	owner := codextui.NewBackendID()
	var got codextui.SessionSelection
	handler := interactiveBackendSwitchHandler(func(selection codextui.SessionSelection) (codextea.SessionResumeResponse, error) {
		got = selection
		return codextea.SessionResumeResponse{
			Summary: &codextui.SessionSummary{ThreadID: selection.Target.ThreadID, CWD: "/dest"},
			Status:  "idle",
		}, nil
	}, local)
	if handler == nil {
		t.Fatal("handler is nil")
	}
	result, err := handler(codextea.BackendSwitchRequest{
		FromBackendID: local,
		FromThreadID:  "thread-source",
		Target:        codextui.SessionTarget{BackendID: owner, ThreadID: "thread-dest", CWD: "/dest"},
		ResumeCWD:     "/dest",
	})
	if err != nil {
		t.Fatalf("switch error = %v", err)
	}
	if got.Kind != codextui.SessionSelectionResume || got.Target.BackendID != local {
		t.Fatalf("resume selection = %#v", got)
	}
	if got.ResumeCWD != "/dest" || got.Target.ThreadID != "thread-dest" {
		t.Fatalf("resume cwd/target = %q/%q", got.ResumeCWD, got.Target.ThreadID)
	}
	if result.Response.Summary == nil || result.Response.Summary.BackendID != owner {
		t.Fatalf("prepared summary = %#v", result.Response.Summary)
	}
}

// TestInteractiveBackendSwitchPropagatesPreparationFailureLikeRust locks Rust's
// "Failed to prepare session backend" handoff: the host reports the resume
// error so the model keeps the current conversation, and an unstamped target is
// reported as unavailable without touching the connection.
func TestInteractiveBackendSwitchPropagatesPreparationFailureLikeRust(t *testing.T) {
	local := codextui.ProcessBackendID()
	handler := interactiveBackendSwitchHandler(func(codextui.SessionSelection) (codextea.SessionResumeResponse, error) {
		return codextea.SessionResumeResponse{}, errors.New("thread not found")
	}, local)
	_, err := handler(codextea.BackendSwitchRequest{
		Target: codextui.SessionTarget{BackendID: codextui.NewBackendID(), ThreadID: "thread-missing"},
	})
	if err == nil || !strings.Contains(err.Error(), "thread not found") {
		t.Fatalf("switch error = %v", err)
	}
	if _, err := handler(codextea.BackendSwitchRequest{}); !errors.Is(err, codextea.ErrBackendSwitchUnavailable) {
		t.Fatalf("unstamped target error = %v", err)
	}
	if nilHandler := interactiveBackendSwitchHandler(nil, local); nilHandler != nil {
		t.Fatal("handler without a resume function is not nil")
	}
}
