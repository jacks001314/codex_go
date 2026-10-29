package appserver

import (
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust #45981 at the behavior level: the thread keeps the session
// layers that defined its permission profiles, so switching profiles through
// `thread/settings/update` stays possible and the switch wins over the session
// default on the next turn. Rust's ConfigManager regression instead pins the
// layer merge directly, because Go has no per-thread config reload to exercise.
func TestRuntimeRouterPermissionSwitchSurvivesSessionDefaultsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadExtras: NewThreadExtraService(),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		Models:       model.NewModelService(nil),
		DefaultCWD:   home,
	})
	router.SetNotificationSink(NewNotificationBuffer())

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD: home,
		Config: map[string]any{
			"default_permissions": "audit",
			"permissions.audit":   map[string]any{"extends": ":read-only"},
		},
	}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID

	nextTurnResolution := func() (*config.SandboxPermissionProfileResolution, error) {
		params := &turn.TurnStartParams{ThreadID: threadID, Prompt: "hello"}
		if err := router.prepareTurnStartParams(params); err != nil {
			return nil, err
		}
		cfg, err := router.effectiveConfigForTurn(params)
		if err != nil {
			return nil, err
		}
		return threadDefaultSandboxPermissionProfile(cfg, params.CWD, params)
	}

	for index, profileID := range []string{":workspace", "audit"} {
		profile := profileID
		update := router.Handle(requestWithParams(t, IntID(int64(index+2)), MethodThreadSettingsUpdate, SettingsUpdateParams{
			ThreadID:    threadID,
			Permissions: &profile,
		}))
		if update.Error != nil {
			t.Fatalf("settings update to %q error: %+v", profileID, update.Error)
		}
		resolution, err := nextTurnResolution()
		if err != nil {
			t.Fatalf("next turn resolution after %q error: %v", profileID, err)
		}
		if resolution == nil || resolution.Profile == nil {
			t.Fatalf("next turn resolution after %q returned no profile", profileID)
		}
		if resolution.ID != profileID {
			t.Fatalf("next turn permission profile = %q, want %q", resolution.ID, profileID)
		}
	}

	// The session-defined profile keeps its read-only filesystem policy after
	// switching away and back, which is what Rust's reload had dropped.
	resolution, err := nextTurnResolution()
	if err != nil || resolution == nil || resolution.Profile == nil {
		t.Fatalf("resolution for session profile error = %v, resolution = %+v", err, resolution)
	}
	if resolution.Profile.SandboxPolicy == nil || resolution.Profile.SandboxPolicy.Kind != sandbox.SandboxReadOnly {
		t.Fatalf("session profile sandbox policy = %+v, want read-only", resolution.Profile.SandboxPolicy)
	}
}
