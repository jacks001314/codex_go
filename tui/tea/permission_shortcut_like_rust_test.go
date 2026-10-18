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
