package tea

import "testing"

// TestServerRequestResolvedDismissesServerViewsLikeRust mirrors Rust #55682:
// resolving an app-server request removes its approval, elicitation, or
// user-input view even when the TUI did not originate the response.
func TestServerRequestResolvedDismissesServerViewsLikeRust(t *testing.T) {
	for _, kind := range []ModalKind{ModalKindApproval, ModalKindElicitation, ModalKindUserInput} {
		t.Run(string(kind), func(t *testing.T) {
			model := builtinPermissionModel(nil)
			model.openModal(ModalRequestMsg{
				ID:              "local-view-1",
				ServerRequestID: "request-1",
				Kind:            kind,
				Title:           "Server request",
				Options:         []ModalOption{{ID: "allow", Label: "Allow"}},
			})
			if model.modal == nil {
				t.Fatal("server request view did not open")
			}
			model.applyServerRequestResolved(ServerRequestResolvedMsg{RequestID: "request-1"})
			if model.modal != nil {
				t.Fatalf("%s modal survived request resolution", kind)
			}
		})
	}
}

// TestServerRequestResolvedSettlesPermissionHoldLikeRust verifies that
// removing the last visible request view releases a prompt held for
// `/permissions` (Rust SettingsSelectionSettled).
func TestServerRequestResolvedSettlesPermissionHoldLikeRust(t *testing.T) {
	model := builtinPermissionModel(nil)
	model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "queued follow-up"}}}
	model.queueAutoSendSuppressed = true
	model.openModal(ModalRequestMsg{
		ID:              "local-approval-view",
		ServerRequestID: "approval-1",
		Kind:            ModalKindApproval,
		Options:         []ModalOption{{ID: "allow", Label: "Allow"}},
	})

	model.applyServerRequestResolved(ServerRequestResolvedMsg{RequestID: "approval-1"})

	if len(model.queued) != 0 {
		t.Fatalf("resolved request did not resume the queue (queue=%d)", len(model.queued))
	}
	if model.queueAutoSendSuppressed {
		t.Fatal("queue autosend stayed suppressed after the request resolved")
	}
}

// TestServerRequestResolvedLeavesLocalViewsLikeRust ensures a server request
// notification cannot dismiss a local picker or unrelated prompt with the same
// id.
func TestServerRequestResolvedLeavesLocalViewsLikeRust(t *testing.T) {
	model := builtinPermissionModel(nil)
	model.queueAutoSendSuppressed = true
	model.openModal(ModalRequestMsg{
		ID:      "request-1",
		Kind:    ModalKindPicker,
		Title:   "Local picker",
		Options: []ModalOption{{ID: "one", Label: "One"}},
	})

	model.applyServerRequestResolved(ServerRequestResolvedMsg{RequestID: "request-1"})

	if model.modal == nil {
		t.Fatal("local picker was dismissed by a server request notification")
	}
	if !model.queueAutoSendSuppressed {
		t.Fatal("unrelated local view released the permissions hold")
	}
}
