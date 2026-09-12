package tea

import (
	"errors"
	"strings"
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

type permissionProfileRecorder struct {
	lists    int
	updates  []string
	listErr  error
	updErr   error
	profiles []chatwidget.CustomPermissionProfile
	// explicit overrides the discovery mode; nil means the server reports
	// explicit profiles (the common case for these tests).
	explicit *bool
}

func (r *permissionProfileRecorder) model(taskRunning bool) *Model {
	recorder := r
	model := NewModel(codextui.NewState(nil), Options{
		Width:  120,
		Height: 40,
		OnListPermissionProfiles: func() ([]chatwidget.CustomPermissionProfile, bool, error) {
			recorder.lists++
			explicit := true
			if recorder.explicit != nil {
				explicit = *recorder.explicit
			}
			return recorder.profiles, explicit, recorder.listErr
		},
		OnUpdateThreadPermissions: func(threadID string, profileID string) error {
			recorder.updates = append(recorder.updates, threadID+"|"+profileID)
			return recorder.updErr
		},
	})
	model.State.SetThreadID("thread-a")
	if taskRunning {
		model.setStatus("running")
	}
	return model
}

func permissionsMenuItemIDs(model *Model) []string {
	ids := make([]string, 0, len(model.permissionItems))
	for _, item := range model.permissionItems {
		ids = append(ids, item.ID)
	}
	return ids
}

// openPermissionsWithProfiles opens the picker and applies the discovery
// result synchronously so tests can select a discovered profile.
func openPermissionsWithProfiles(model *Model) {
	cmd := model.openPermissionsMenu()
	if cmd == nil {
		return
	}
	if message, ok := cmd().(permissionProfilesLoadedMsg); ok {
		model.applyPermissionProfilesLoaded(message)
	}
}

// TestPermissionsMenuListsServerProfiles covers Rust #43340: the picker lists
// the connected server's named profiles, and selecting one asks the server
// through thread/settings/update, tracking the selection until confirmation.
func TestPermissionsMenuListsServerProfiles(t *testing.T) {
	recorder := &permissionProfileRecorder{profiles: []chatwidget.CustomPermissionProfile{
		{ID: "read-only-remote", Description: "Read only", Allowed: true},
		{ID: "trusted", Allowed: false},
	}}
	model := recorder.model(false)
	cmd := model.openPermissionsMenu()
	if cmd == nil {
		t.Fatal("opening /permissions should discover the server's profiles")
	}
	updated, _ := model.Update(cmd())
	model = updated.(*Model)
	if recorder.lists != 1 {
		t.Fatalf("list calls = %d", recorder.lists)
	}
	ids := permissionsMenuItemIDs(model)
	if !containsString(ids, "trusted") || !containsString(ids, "read-only-remote") {
		t.Fatalf("picker items = %#v", ids)
	}

	// Selecting a named profile goes through the server, not local state.
	model.applyPermissionsModalOption("trusted")
	if len(recorder.updates) != 1 || recorder.updates[0] != "thread-a|trusted" {
		t.Fatalf("update calls = %#v", recorder.updates)
	}
	if model.pendingServerProfile != "trusted" {
		t.Fatalf("pending profile = %q", model.pendingServerProfile)
	}
	if !strings.Contains(model.notice, "Permission selection requested: trusted") {
		t.Fatalf("notice = %q", model.notice)
	}
	if model.State.Sandbox == "trusted" {
		t.Fatal("a named server profile must not be applied locally before confirmation")
	}

	// The server's confirmation clears the pending selection.
	model.applyThreadSettingsUpdated(ThreadSettingsUpdatedMsg{
		ThreadID: "thread-a",
		Settings: appserver.Settings{SandboxPolicy: "workspace-write", ActivePermissionProfile: stringPtrTea("trusted")},
	})
	if model.pendingServerProfile != "" {
		t.Fatalf("pending profile after confirmation = %q", model.pendingServerProfile)
	}
}

// TestPermissionsMenuSelectionGuards covers the pending/failure/unsupported
// paths from Rust #43340.
func TestPermissionsMenuSelectionGuards(t *testing.T) {
	// A pending selection blocks further permission and directory changes.
	pending := (&permissionProfileRecorder{profiles: []chatwidget.CustomPermissionProfile{{ID: "trusted", Allowed: true}}}).model(false)
	openPermissionsWithProfiles(pending)
	pending.pendingServerProfile = "trusted"
	pending.State.Sandbox = "read-only"
	pending.applyPermissionsModalOption("trusted")
	if !strings.Contains(pending.notice, "Wait for permissions to update before changing permissions.") {
		t.Fatalf("pending notice = %q", pending.notice)
	}
	pending.applyWorkingDirectoryChangeCommand("D:/other")
	if !strings.Contains(modelMessageText(pending), "Wait for permissions to update before changing directories.") {
		t.Fatalf("pending directory message missing: %q", modelMessageText(pending))
	}

	// A running turn blocks the change.
	running := (&permissionProfileRecorder{profiles: []chatwidget.CustomPermissionProfile{{ID: "trusted", Allowed: true}}}).model(true)
	openPermissionsWithProfiles(running)
	running.applyPermissionsModalOption("trusted")
	if !strings.Contains(running.notice, "Wait for the current turn to finish before changing permissions.") {
		t.Fatalf("running notice = %q", running.notice)
	}

	// An unsupported server reports the upgrade hint and leaves nothing pending.
	unsupported := (&permissionProfileRecorder{
		profiles: []chatwidget.CustomPermissionProfile{{ID: "trusted", Allowed: true}},
		updErr:   ErrNamedPermissionProfilesUnsupported,
	}).model(false)
	openPermissionsWithProfiles(unsupported)
	unsupported.applyPermissionsModalOption("trusted")
	if !strings.Contains(unsupported.notice, "Named profiles require a newer app server.") {
		t.Fatalf("unsupported notice = %q", unsupported.notice)
	}
	if unsupported.pendingServerProfile != "" {
		t.Fatalf("unsupported pending = %q", unsupported.pendingServerProfile)
	}

	// A failed selection preserves the current permissions and reports the error.
	failed := (&permissionProfileRecorder{
		profiles: []chatwidget.CustomPermissionProfile{{ID: "trusted", Allowed: true}},
		updErr:   errors.New("boom"),
	}).model(false)
	failed.State.Sandbox = "read-only"
	openPermissionsWithProfiles(failed)
	failed.applyPermissionsModalOption("trusted")
	if !strings.Contains(failed.notice, "Failed to select permissions: boom") {
		t.Fatalf("failure notice = %q", failed.notice)
	}
	if failed.State.Sandbox != "read-only" {
		t.Fatalf("sandbox = %q, want the previous profile preserved", failed.State.Sandbox)
	}
}

// TestNamedRemoteProfileBlocksDirectoryChanges covers the active-profile guard
// (Rust #43340).
func TestNamedRemoteProfileBlocksDirectoryChanges(t *testing.T) {
	model := (&permissionProfileRecorder{}).model(false)
	model.State.Sandbox = "trusted"
	model.applyWorkingDirectoryChangeCommand("D:/other")
	if !strings.Contains(modelMessageText(model), "Changing directories with a named profile is not supported.") {
		t.Fatalf("directory guard missing: %q", modelMessageText(model))
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func stringPtrTea(value string) *string { return &value }
