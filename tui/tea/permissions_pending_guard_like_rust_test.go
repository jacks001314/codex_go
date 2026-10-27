package tea

import (
	"strings"
	"testing"

	"codex_go/appserver"
)

// Rust #55682: `/permissions` holds queued prompts and rejects a second
// permission change while a server profile selection is still pending.
func TestPermissionsCommandHoldsQueueAndRejectsPendingChangeLikeRust(t *testing.T) {
	var selections []PermissionSelection
	model := builtinPermissionModel(&selections)
	model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "queued follow-up"}}}
	model.pendingServerProfile = ":workspace"

	model.applyPermissionsCommand("")

	if model.modal != nil {
		t.Fatal("a pending permission change must not open a second permissions view")
	}
	if !strings.Contains(model.notice, "Wait for permissions to update") {
		t.Fatalf("notice = %q", model.notice)
	}
	if !model.queueAutoSendSuppressed {
		t.Fatal("the queued prompt hold must survive a rejected permission change")
	}
	if len(model.queued) != 1 {
		t.Fatalf("queued prompts = %d, want the hold to keep one", len(model.queued))
	}
}

// Rust #55682: the turn-completion flush cannot bypass the permissions hold.
// Once the covering settings update arrives, the hold is released and the
// queued prompt is submitted.
func TestPermissionHoldReleasesQueuedPromptAfterSettingsSettleLikeRust(t *testing.T) {
	var selections []PermissionSelection
	model := builtinPermissionModel(&selections)
	model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "queued follow-up"}}}
	model.queueAutoSendSuppressed = true
	model.pendingServerProfile = ":workspace"

	if cmd := model.submitNextQueued(); cmd != nil || len(model.queued) != 1 {
		t.Fatalf("queued prompt resumed while permissions were pending (cmd=%v, queue=%d)", cmd, len(model.queued))
	}

	profile := ":workspace"
	model.applyThreadSettingsUpdated(ThreadSettingsUpdatedMsg{
		ThreadID: "thread-a",
		Settings: appserver.Settings{
			ActivePermissionProfile: &profile,
			ApprovalPolicy:          "on-request",
			ApprovalsReviewer:       "user",
		},
	})
	if len(model.queued) != 0 {
		t.Fatalf("settled permissions did not resume the queue (queue=%d)", len(model.queued))
	}
	if model.queueAutoSendSuppressed {
		t.Fatal("queue autosend stayed suppressed after the settings settled")
	}
}

// Rust #55682: cancelling the permissions menu settles the hold as well.
func TestPermissionModalCancelSettlesQueuedPromptLikeRust(t *testing.T) {
	var selections []PermissionSelection
	model := builtinPermissionModel(&selections)
	model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "queued follow-up"}}}

	model.applyPermissionsCommand("")
	if model.modal == nil {
		t.Fatal("permissions command did not open a view")
	}
	model.respondModal(true)
	if len(model.queued) != 0 {
		t.Fatalf("cancelled permissions did not resume the queue (queue=%d)", len(model.queued))
	}
	if model.queueAutoSendSuppressed || model.permissionPopupPending {
		t.Fatal("permission hold flags survived the cancelled menu")
	}
}

// Rust #55682: removing a resolved app-server request that closed the last
// visible modal settles the permissions hold so queued prompts resume.
func TestElicitationAbandonedSettlesPermissionHoldLikeRust(t *testing.T) {
	var selections []PermissionSelection
	model := builtinPermissionModel(&selections)
	model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "queued follow-up"}}}
	model.queueAutoSendSuppressed = true
	model.openModal(ModalRequestMsg{
		ID:      "elicit-1",
		Kind:    ModalKindElicitation,
		Title:   "MCP request",
		Options: []ModalOption{{ID: "accept", Label: "Allow"}},
	})
	if model.modal == nil {
		t.Fatal("elicitation modal did not open")
	}
	model.applyElicitationAbandoned(ElicitationAbandonedMsg{RequestID: "elicit-1"})
	if model.modal != nil {
		t.Fatal("abandoned elicitation modal was not dismissed")
	}
	if len(model.queued) != 0 {
		t.Fatalf("closed app-server request did not resume the queue (queue=%d)", len(model.queued))
	}
	if model.queueAutoSendSuppressed {
		t.Fatal("queue autosend stayed suppressed after the request closed")
	}
}
