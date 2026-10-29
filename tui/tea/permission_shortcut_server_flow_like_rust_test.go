package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

func serverPermissionShortcutModel(t *testing.T, selections *[]PermissionSelection) *Model {
	t.Helper()
	keymap := codextui.NewKeymapConfig()
	if err := keymap.Set("chat", "previous_permission_mode", []string{"f7"}); err != nil {
		t.Fatalf("set previous permission shortcut: %v", err)
	}
	if err := keymap.Set("chat", "next_permission_mode", []string{"f10"}); err != nil {
		t.Fatalf("set next permission shortcut: %v", err)
	}
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	state.Sandbox = chatwidget.WorkspaceProfile
	state.ApprovalPolicy = string(chatwidget.ApprovalOnRequest)
	return NewModel(state, Options{
		Width:        100,
		Height:       30,
		KeymapConfig: keymap,
		OnUpdateThreadPermissions: func(threadID string, selection PermissionSelection) error {
			*selections = append(*selections, selection)
			return nil
		},
		OnWriteSettings: func([]SettingsEdit) (SettingsWriteResult, error) {
			t.Fatal("permission shortcut must not persist config")
			return SettingsWriteResult{}, nil
		},
	})
}

// Mirrors Rust #46013: a session-only permission shortcut in a connected thread
// routes through the shared selection flow, so local permissions keep the
// confirmed profile until the server's ThreadSettingsUpdated notification
// arrives, the pending selection clears then, and nothing is written to config.
func TestModelPermissionShortcutWaitsForServerConfirmationLikeRust(t *testing.T) {
	var selections []PermissionSelection
	model := serverPermissionShortcutModel(t, &selections)

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF7})
	if len(selections) != 1 {
		t.Fatalf("server updates = %#v, want one shortcut selection", selections)
	}
	if got := selections[0].ProfileID; got != chatwidget.ReadOnlyProfile {
		t.Fatalf("requested profile = %q, want %q", got, chatwidget.ReadOnlyProfile)
	}
	if model.State.Sandbox != chatwidget.WorkspaceProfile {
		t.Fatalf("sandbox = %q, want the confirmed profile while the request is pending", model.State.Sandbox)
	}
	if model.pendingServerProfile != chatwidget.ReadOnlyProfile {
		t.Fatalf("pending profile = %q, want %q", model.pendingServerProfile, chatwidget.ReadOnlyProfile)
	}
	if !strings.Contains(model.notice, "Permission selection requested: ") {
		t.Fatalf("notice = %q, want the requested-selection message", model.notice)
	}

	profile := chatwidget.ReadOnlyProfile
	model.applyThreadSettingsUpdated(ThreadSettingsUpdatedMsg{
		ThreadID: "thread-a",
		Settings: appserver.Settings{ActivePermissionProfile: &profile},
	})
	if model.pendingServerProfile != "" {
		t.Fatalf("pending profile after confirmation = %q", model.pendingServerProfile)
	}
	if model.State.Sandbox != chatwidget.ReadOnlyProfile {
		t.Fatalf("sandbox after confirmation = %q, want %q", model.State.Sandbox, chatwidget.ReadOnlyProfile)
	}
}

// Mirrors Rust #46013's rejection snapshots: an unsupported server and a server
// error both reuse the shared selection messages, leave permissions unchanged,
// and do not leave a pending selection behind.
func TestModelPermissionShortcutRejectionsLikeRust(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "unsupported server",
			err:  ErrNamedPermissionProfilesUnsupported,
			want: "Permission selection requires a newer app server.",
		},
		{
			name: "server error",
			err:  errors.New("thread not found"),
			want: "Failed to select permissions: thread not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var selections []PermissionSelection
			model := serverPermissionShortcutModel(t, &selections)
			model.onUpdateThreadPermissions = func(string, PermissionSelection) error {
				return tc.err
			}

			model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF7})
			if model.State.Sandbox != chatwidget.WorkspaceProfile {
				t.Fatalf("sandbox = %q, want the confirmed profile after a rejection", model.State.Sandbox)
			}
			if model.pendingServerProfile != "" {
				t.Fatalf("pending profile after a rejection = %q", model.pendingServerProfile)
			}
			if model.notice != tc.want {
				t.Fatalf("notice = %q, want %q", model.notice, tc.want)
			}
		})
	}
}
