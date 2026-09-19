package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust's `thread_settings_update_preserves_session_profiles` (#45981):
// switching away from and back to a profile that the thread defined through its
// start-time session config must keep reporting the requested profile. Rust had
// reloaded configuration without the thread's session layers, so a session-
// defined profile was unavailable to `thread/settings/update`.
func TestRuntimeRouterThreadSettingsUpdatePreservesSessionProfilesLikeRust(t *testing.T) {
	cases := []struct {
		name              string
		profileOnDisk     bool
		topLevelSelection bool
	}{
		{name: "session-only", profileOnDisk: false, topLevelSelection: false},
		{name: "disk-and-session", profileOnDisk: true, topLevelSelection: false},
		{name: "top-level-selection", profileOnDisk: true, topLevelSelection: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			configBody := ""
			if tc.profileOnDisk {
				configBody = "default_permissions = ':read-only'\n[permissions.audit]\nextends = ':read-only'\n"
			}
			if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
				t.Fatal(err)
			}
			store := session.NewStore(filepath.Join(home, "sessions"))
			sink := NewNotificationBuffer()
			router := NewRuntimeRouter(RuntimeServices{
				ThreadRouter: NewRouter(store),
				ThreadExtras: NewThreadExtraService(),
				Config:       config.NewConfigService(home),
				Turns:        turn.NewTurnService(),
				ThreadStatus: NewThreadStatusManager(),
				Models:       model.NewModelService(nil),
				DefaultCWD:   home,
			})
			router.SetNotificationSink(sink)

			sessionConfig := map[string]any{
				"default_permissions":        "audit",
				"features.guardian_approval": true,
			}
			if !tc.profileOnDisk {
				sessionConfig["permissions.audit"] = map[string]any{"extends": ":read-only"}
			}
			var permissions *string
			if tc.topLevelSelection {
				delete(sessionConfig, "default_permissions")
				value := "audit"
				permissions = &value
			}
			start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
				CWD:         home,
				Config:      sessionConfig,
				Permissions: permissions,
			}))
			if start.Error != nil {
				t.Fatalf("thread start error: %+v", start.Error)
			}
			threadID := start.Result.(*ThreadStartResponse).Thread.ID

			nextID := int64(2)
			for _, profileID := range []string{":workspace", "audit"} {
				profile := profileID
				update := router.Handle(requestWithParams(t, IntID(nextID), MethodThreadSettingsUpdate, SettingsUpdateParams{
					ThreadID:    threadID,
					Permissions: &profile,
				}))
				nextID++
				if update.Error != nil {
					t.Fatalf("settings update to %q error: %+v", profileID, update.Error)
				}
				applied := lastThreadSettingsUpdated(t, sink)
				if applied == nil || applied.ActivePermissionProfile == nil {
					t.Fatalf("settings update to %q produced no active permission profile: %+v", profileID, applied)
				}
				if got := *applied.ActivePermissionProfile; got != profileID {
					t.Fatalf("applied active permission profile = %q, want %q", got, profileID)
				}
			}

			// The switched-back profile must stay resolvable for the next turn:
			// the turn path merges the record's session config before resolving
			// the profile, which is what Rust's `load_permission_config_for_thread`
			// preserves (#45981).
			record, err := store.Read(session.ThreadID(threadID), true, true)
			if err != nil {
				t.Fatalf("read started thread error: %v", err)
			}
			cfg, err := router.effectiveConfigForTurn(&turn.TurnStartParams{
				ThreadID: threadID,
				CWD:      home,
				Config:   mergeTurnConfigOverrides(threadRecordConfigOverrides(record), nil),
			})
			if err != nil {
				t.Fatalf("effective config error: %v", err)
			}
			resolution, err := cfg.ResolveSandboxPermissionProfile("audit", home)
			if err != nil {
				t.Fatalf("resolve session profile audit error: %v", err)
			}
			if resolution == nil || resolution.Profile == nil {
				t.Fatal("session profile audit did not resolve for the next turn")
			}
		})
	}
}

func lastThreadSettingsUpdated(t *testing.T, sink *NotificationBuffer) *Settings {
	t.Helper()
	if sink == nil {
		t.Fatal("notification sink is nil")
	}
	var found *Settings
	for _, notification := range sink.List() {
		if notification == nil || notification.Method != NotificationThreadSettingsUpdated {
			continue
		}
		payload, ok := notification.Params.(*SettingsUpdatedNotification)
		if !ok {
			continue
		}
		settings := payload.ThreadSettings
		found = &settings
	}
	return found
}
