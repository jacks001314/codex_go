package tea

import (
	"strings"
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

func builtinPermissionModel(recorder *[]PermissionSelection) *Model {
	model := NewModel(codextui.NewState(nil), Options{
		Width:  120,
		Height: 40,
		OnUpdateThreadPermissions: func(threadID string, selection PermissionSelection) error {
			*recorder = append(*recorder, selection)
			return nil
		},
	})
	model.State.SetThreadID("thread-a")
	model.State.Sandbox = chatwidget.ReadOnlyProfile
	model.State.ApprovalPolicy = "on-request"
	return model
}

// TestBuiltinPermissionSelectionRoutesThroughAppServerLikeRust covers Rust
// #46008: a connected thread sends built-in presets through
// thread/settings/update and adopts the confirmed settings rather than applying
// the picker result locally.
func TestBuiltinPermissionSelectionRoutesThroughAppServerLikeRust(t *testing.T) {
	var selections []PermissionSelection
	model := builtinPermissionModel(&selections)
	model.openPermissionsMenu()
	model.applyPermissionsModalOption("auto:user")

	if len(selections) != 1 {
		t.Fatalf("server updates = %#v, want one built-in selection", selections)
	}
	got := selections[0]
	if got.ProfileID != ":workspace" || got.ApprovalPolicy != "on-request" ||
		got.ApprovalsReviewer != "user" || got.DisplayLabel != "Ask for approval" {
		t.Fatalf("selection = %#v", got)
	}
	if model.State.Sandbox != ":read-only" {
		t.Fatalf("sandbox = %q, want the previous profile until confirmation", model.State.Sandbox)
	}
	if model.pendingServerProfile != ":workspace" {
		t.Fatalf("pending profile = %q", model.pendingServerProfile)
	}
	if !strings.Contains(model.notice, "Permission selection requested: Ask for approval") {
		t.Fatalf("notice = %q", model.notice)
	}

	profile := ":workspace"
	model.applyThreadSettingsUpdated(ThreadSettingsUpdatedMsg{
		ThreadID: "thread-a",
		Settings: appserver.Settings{
			ActivePermissionProfile: &profile,
			ApprovalPolicy:          "on-request",
			ApprovalsReviewer:       "auto_review",
		},
	})
	if model.pendingServerProfile != "" {
		t.Fatalf("pending profile after confirmation = %q", model.pendingServerProfile)
	}
	if model.State.Sandbox != ":workspace" || model.State.ApprovalPolicy != "on-request" {
		t.Fatalf("confirmed state = (%q, %q)", model.State.Sandbox, model.State.ApprovalPolicy)
	}
	if model.approvalsReviewer != "auto_review" {
		t.Fatalf("reviewer = %q, want the server's confirmed value", model.approvalsReviewer)
	}
}

// TestBuiltinPermissionSelectionAllowedDuringRunningTurnLikeRust covers Rust
// #46008: built-in selections may be requested during a running turn, while
// named server profiles still wait for the turn to finish.
func TestBuiltinPermissionSelectionAllowedDuringRunningTurnLikeRust(t *testing.T) {
	var selections []PermissionSelection
	model := builtinPermissionModel(&selections)
	model.State.Sandbox = ":workspace"
	model.setStatus("running")
	model.openPermissionsMenu()
	if len(model.permissionItems) == 0 {
		t.Fatal("permission picker did not open")
	}
	model.applyPermissionsModalOption("read-only:user")

	if len(selections) != 1 || selections[0].ProfileID != ":read-only" {
		t.Fatalf("server updates = %#v, want the built-in selection", selections)
	}
	if model.pendingServerProfile != ":read-only" {
		t.Fatalf("pending profile = %q", model.pendingServerProfile)
	}
}

// TestMatchingBuiltinPermissionSelectionSkipsServerLikeRust covers Rust
// #46008's already-selected shortcut: an unchanged built-in selection does not
// wait for a settings notification that may never arrive.
func TestMatchingBuiltinPermissionSelectionSkipsServerLikeRust(t *testing.T) {
	var selections []PermissionSelection
	model := builtinPermissionModel(&selections)
	model.State.Sandbox = ":workspace"

	model.openPermissionsMenu()
	model.applyPermissionsModalOption("auto:user")

	if len(selections) != 0 {
		t.Fatalf("server updates = %#v, want none for a matching selection", selections)
	}
	if model.pendingServerProfile != "" {
		t.Fatalf("pending profile = %q, want none", model.pendingServerProfile)
	}
}

// TestPendingPermissionSelectionBlocksTaskSwitchLikeRust covers Rust #46008's
// root-switch guard for /new and resuming another conversation.
func TestPendingPermissionSelectionBlocksTaskSwitchLikeRust(t *testing.T) {
	var selections []PermissionSelection
	model := builtinPermissionModel(&selections)
	model.pendingServerProfile = ":workspace"

	model.startFreshNamedSession("", "Started a new local thread.")
	if model.State.ThreadID != "thread-a" {
		t.Fatalf("thread id = %q, /new switched while permissions were pending", model.State.ThreadID)
	}
	if !strings.Contains(modelMessageText(model), "Wait for permissions to update before switching tasks.") {
		t.Fatalf("missing switch guard: %q", modelMessageText(model))
	}

	decision, notice, _ := model.applySessionSelection(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{ThreadID: "thread-b"},
	})
	if decision != nil || notice != "" {
		t.Fatalf("resume decision=%#v notice=%q, want a blocked switch", decision, notice)
	}
	if model.State.ThreadID != "thread-a" {
		t.Fatalf("resume switched to %q while permissions were pending", model.State.ThreadID)
	}
	if !strings.Contains(modelMessageText(model), "Wait for permissions to update before switching tasks.") {
		t.Fatalf("missing resume guard: %q", modelMessageText(model))
	}
}
