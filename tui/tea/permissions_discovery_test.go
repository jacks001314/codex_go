package tea

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// TestPermissionsMenuShowsLoadingView covers Rust #43340's request flow: opening
// the picker while discovery runs shows the "Update Model Permissions" loading
// view, which the result replaces.
func TestPermissionsMenuShowsLoadingView(t *testing.T) {
	recorder := &permissionProfileRecorder{profiles: []chatwidget.CustomPermissionProfile{{ID: "trusted", Allowed: true}}}
	model := recorder.model(false)
	command := model.openPermissionsMenu()
	if command == nil {
		t.Fatal("opening /permissions should discover the server's profiles")
	}
	if model.modal == nil || model.modal.title != "Update Model Permissions" {
		t.Fatalf("loading modal = %#v", model.modal)
	}
	if len(model.modal.options) != 1 || !model.modal.options[0].Disabled ||
		!strings.Contains(model.modal.options[0].Label, "Loading permission profiles") {
		t.Fatalf("loading options = %#v", model.modal.options)
	}
	updated, _ := model.Update(command())
	model = updated.(*Model)
	if !containsString(permissionsMenuItemIDs(model), "trusted") {
		t.Fatalf("picker items = %#v, want the discovered profile", permissionsMenuItemIDs(model))
	}
}

// TestPermissionsMenuFallsBackToLegacyPresets covers the server reporting no
// explicit profiles (Rust open_legacy_permissions_popup), after which the picker
// does not re-run discovery.
func TestPermissionsMenuFallsBackToLegacyPresets(t *testing.T) {
	explicit := false
	recorder := &permissionProfileRecorder{explicit: &explicit}
	model := recorder.model(false)
	command := model.openPermissionsMenu()
	if command == nil {
		t.Fatal("the first open should discover")
	}
	updated, _ := model.Update(command())
	model = updated.(*Model)
	if model.permissionProfilesExplicit {
		t.Fatal("explicit-profile mode should be off")
	}
	if recorder.lists != 1 {
		t.Fatalf("discovery calls = %d", recorder.lists)
	}
	if command := model.openPermissionsMenu(); command != nil {
		t.Fatal("legacy mode must not re-run discovery")
	}
	if recorder.lists != 1 {
		t.Fatalf("discovery calls after reopen = %d", recorder.lists)
	}
}

// TestPermissionsMenuDiscoveryErrorShowsRetry covers Rust #43340's failed
// discovery view: the raw error is the subtitle and Retry re-runs the request.
func TestPermissionsMenuDiscoveryErrorShowsRetry(t *testing.T) {
	recorder := &permissionProfileRecorder{
		profiles: []chatwidget.CustomPermissionProfile{{ID: "trusted", Allowed: true}},
		listErr:  errors.New("boom"),
	}
	model := recorder.model(false)
	command := model.openPermissionsMenu()
	if command == nil {
		t.Fatal("opening /permissions should discover")
	}
	updated, _ := model.Update(command())
	model = updated.(*Model)
	if model.modal == nil || model.modal.title != "Update Model Permissions" || model.modal.body != "boom" {
		t.Fatalf("retry modal = %#v", model.modal)
	}
	if len(model.modal.options) != 1 || model.modal.options[0].ID != permissionsRetryOptionID {
		t.Fatalf("retry options = %#v", model.modal.options)
	}
	recorder.listErr = nil
	retry := model.applyPermissionsModalOption(permissionsRetryOptionID)
	if retry == nil {
		t.Fatal("Retry must re-run discovery")
	}
	updated, _ = model.Update(retry())
	model = updated.(*Model)
	if recorder.lists != 2 {
		t.Fatalf("discovery calls after Retry = %d", recorder.lists)
	}
	if !containsString(permissionsMenuItemIDs(model), "trusted") {
		t.Fatalf("picker items after Retry = %#v", permissionsMenuItemIDs(model))
	}
}

// TestPendingPermissionSelectionBlocksForkAndSide covers Rust #43340's pending
// selection guards for forking and side conversations.
func TestPendingPermissionSelectionBlocksForkAndSide(t *testing.T) {
	forks := 0
	sideStarts := 0
	model := NewModel(codextui.NewState(nil), Options{
		Width:  120,
		Height: 40,
		OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
			forks++
			return &codextui.SessionSummary{ThreadID: "forked"}, nil
		},
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			sideStarts++
			return SideStartResponse{}, nil
		},
	})
	model.State.SetThreadID("thread-a")
	model.pendingServerProfile = "trusted"

	if command := model.applyForkCurrentSession(""); command != nil {
		t.Fatal("fork returned a command while a selection is pending")
	}
	if forks != 0 {
		t.Fatalf("fork calls = %d, want none", forks)
	}
	if !strings.Contains(modelMessageText(model), "Wait for permissions to update before forking.") {
		t.Fatalf("fork message missing: %q", modelMessageText(model))
	}

	if command := model.startSideConversation("/side", "hello"); command != nil {
		t.Fatal("side start returned a command while a selection is pending")
	}
	if sideStarts != 0 {
		t.Fatalf("side starts = %d, want none", sideStarts)
	}
	if !strings.Contains(modelMessageText(model), "Wait for permissions to update before forking.") {
		t.Fatalf("side message missing: %q", modelMessageText(model))
	}
}
