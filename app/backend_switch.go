package app

import (
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// interactiveBackendSwitchHandler prepares a saved conversation owned by
// another backend (Rust #58457, tui/src/app/backend_selection.rs
// resume_backend_session).
//
// An interactive Go runtime holds exactly one connection: one app-server
// endpoint or the local session store. The owning backend is therefore reached
// through that connection - the resume runs on it and the returned summary is
// re-stamped with the owner so the TUI records the thread's real backend
// (Rust backends.displayed). A thread the connection cannot serve fails the
// preparation, and the TUI keeps the displayed conversation, matching Rust's
// "Failed to prepare session backend: ..." handoff.
func interactiveBackendSwitchHandler(resume codextea.SessionResumeFunc, local codextui.BackendID) codextea.BackendSwitchFunc {
	if resume == nil {
		return nil
	}
	return func(request codextea.BackendSwitchRequest) (codextea.BackendSwitchResult, error) {
		owner := request.Target.BackendID
		if owner.IsZero() {
			return codextea.BackendSwitchResult{}, codextea.ErrBackendSwitchUnavailable
		}
		selection := codextui.SessionSelection{
			Kind:      codextui.SessionSelectionResume,
			Target:    request.Target,
			ResumeCWD: request.ResumeCWD,
		}
		// The resume handler guards its own connection, but this target was
		// listed elsewhere: re-stamp it with the connection that will actually
		// serve the replay.
		selection.Target.BackendID = local
		response, err := resume(selection)
		if err != nil {
			return codextea.BackendSwitchResult{}, err
		}
		if response.Summary != nil {
			summary := *response.Summary
			summary.BackendID = owner
			response.Summary = &summary
		}
		return codextea.BackendSwitchResult{Response: response}, nil
	}
}
