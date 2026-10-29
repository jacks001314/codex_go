package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

func permissionShortcutModel(t *testing.T) *Model {
	t.Helper()
	keymap := codextui.NewKeymapConfig()
	if err := keymap.Set("chat", "previous_permission_mode", []string{"f7"}); err != nil {
		t.Fatalf("set previous permission shortcut: %v", err)
	}
	if err := keymap.Set("chat", "next_permission_mode", []string{"f10"}); err != nil {
		t.Fatalf("set next permission shortcut: %v", err)
	}
	state := codextui.NewState(nil)
	state.Sandbox = chatwidget.WorkspaceProfile
	state.ApprovalPolicy = string(chatwidget.ApprovalOnRequest)
	return NewModel(state, Options{
		Width:        100,
		Height:       30,
		KeymapConfig: keymap,
		OnWriteSettings: func([]SettingsEdit) (SettingsWriteResult, error) {
			t.Fatal("permission shortcut must not persist config")
			return SettingsWriteResult{}, nil
		},
	})
}

func TestModelPermissionShortcutsCycleSessionOnlyLikeRust(t *testing.T) {
	model := permissionShortcutModel(t)
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF7})
	if model.State.Sandbox != chatwidget.ReadOnlyProfile {
		t.Fatalf("previous permission mode = %q, want read-only", model.State.Sandbox)
	}
	if view := strings.ToLower(model.View()); !strings.Contains(view, "permissions updated to read only") {
		t.Fatalf("previous permission update missing from transcript:\n%s", model.View())
	}

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF10})
	if model.State.Sandbox != chatwidget.WorkspaceProfile {
		t.Fatalf("next permission mode = %q, want workspace", model.State.Sandbox)
	}
}

func TestModelPermissionShortcutFullAccessStillRequiresConfirmationLikeRust(t *testing.T) {
	model := permissionShortcutModel(t)
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF7})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF7})

	if model.State.Sandbox != chatwidget.ReadOnlyProfile {
		t.Fatalf("full-access shortcut changed state before confirmation: %q", model.State.Sandbox)
	}
	if model.modal == nil || !strings.Contains(strings.ToLower(model.modal.title), "full access") {
		t.Fatalf("full-access confirmation modal = %#v", model.modal)
	}
}

// Mirrors Rust #50140: shortcuts cycle against the server's cached permission
// catalog. Without one they open the loading picker and reuse an outstanding
// discovery fetch instead of cycling local presets the server may forbid.
func TestModelPermissionShortcutWaitsForServerCatalogLikeRust(t *testing.T) {
	model := permissionShortcutModel(t)
	fetches := 0
	model.onListPermissionProfiles = func() ([]chatwidget.CustomPermissionProfile, bool, error) {
		fetches++
		return nil, false, nil
	}
	_, cmd := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF7})
	if model.State.Sandbox != chatwidget.WorkspaceProfile {
		t.Fatalf("shortcut changed state before the server catalog loaded: %q", model.State.Sandbox)
	}
	if model.modal == nil || model.modal.kind != ModalKindPermissions {
		t.Fatalf("shortcut did not open the permissions picker: %#v", model.modal)
	}
	if cmd == nil {
		t.Fatal("shortcut did not start permission discovery")
	}
	msg := cmd()
	if fetches != 1 {
		t.Fatalf("discovery fetches = %d, want 1", fetches)
	}
	// A second press while the fetch is in flight must reuse it.
	if _, retry := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF7}); retry != nil {
		t.Fatal("shortcut started a second discovery fetch")
	}
	if fetches != 1 {
		t.Fatalf("discovery fetches = %d, want the outstanding fetch to be reused", fetches)
	}

	model.applyPermissionProfilesLoaded(msg.(permissionProfilesLoadedMsg))
	model.closePermissionsModal()
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF7})
	if model.State.Sandbox != chatwidget.ReadOnlyProfile {
		t.Fatalf("next permission mode = %q, want read-only once the catalog is cached", model.State.Sandbox)
	}
}

// Mirrors Rust #50140: the cached catalog is cleared when the session changes.
func TestModelPermissionShortcutCacheClearedOnThreadSwitchLikeRust(t *testing.T) {
	model := permissionShortcutModel(t)
	model.permissionProfiles = []chatwidget.CustomPermissionProfile{{ID: "managed", Allowed: true}}
	model.permissionProfilesDiscovered = true
	model.permissionProfilesExplicit = true
	model.resetThreadScopedState()
	if model.permissionProfilesDiscovered || model.permissionProfilesExplicit || len(model.permissionProfiles) != 0 {
		t.Fatalf("permission catalog cache survived a thread switch: %#v", model.permissionProfiles)
	}
}

// Mirrors Rust #50140: an account change also drops the cached catalog.
func TestModelPermissionShortcutCacheClearedOnAccountChangeLikeRust(t *testing.T) {
	model := permissionShortcutModel(t)
	model.State.AccountDisplay = "first@example.com"
	model.permissionProfilesDiscovered = true
	model.permissionProfiles = []chatwidget.CustomPermissionProfile{{ID: "managed", Allowed: true}}
	model.applyAccountStatusUpdated(AccountStatusUpdatedMsg{Display: "second@example.com"})
	if model.permissionProfilesDiscovered || len(model.permissionProfiles) != 0 {
		t.Fatalf("permission catalog cache survived an account change: %#v", model.permissionProfiles)
	}
}
