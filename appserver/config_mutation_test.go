package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// TestConfigEditsAreSessionDefaultsOnlyMatchesRust covers Rust's
// session_defaults_only check: only the five launch-default keys (and only when
// every edit is one of them) leave the running configuration untouched.
func TestConfigEditsAreSessionDefaultsOnlyMatchesRust(t *testing.T) {
	cases := []struct {
		name  string
		edits []config.ConfigEdit
		want  bool
	}{
		{name: "empty", edits: nil, want: false},
		{
			name: "launch defaults",
			edits: []config.ConfigEdit{
				{KeyPath: "model"},
				{KeyPath: "model_reasoning_effort"},
				{KeyPath: "plan_mode_reasoning_effort"},
				{KeyPath: "service_tier"},
				{KeyPath: "personality"},
			},
			want: true,
		},
		{name: "trimmed key", edits: []config.ConfigEdit{{KeyPath: " model "}}, want: true},
		{
			name:  "mixed with a feature flag",
			edits: []config.ConfigEdit{{KeyPath: "model"}, {KeyPath: "features.network_proxy"}},
			want:  false,
		},
		{name: "unrelated key", edits: []config.ConfigEdit{{KeyPath: "tui.theme"}}, want: false},
	}
	for _, testCase := range cases {
		if got := configEditsAreSessionDefaultsOnly(testCase.edits); got != testCase.want {
			t.Fatalf("%s: configEditsAreSessionDefaultsOnly = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

// TestConfigBatchWriteHonorsReloadUserConfigLikeRust covers Rust
// config_processor: a session-defaults-only write is inert, any other write
// clears the derived plugin/skill caches, and reloadUserConfig additionally
// refreshes the loaded threads' config-derived runtime state.
func TestConfigBatchWriteHonorsReloadUserConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	skills := NewSkillsService(nil)
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions"))),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Skills:       skills,
		ThreadStatus: NewThreadStatusManager(),
	})
	write := func(edits []config.ConfigEdit, reload bool) {
		t.Helper()
		response := router.Handle(requestWithParams(t, IntID(1), MethodConfigBatchWrite, config.ConfigBatchWriteParams{
			Edits:            edits,
			ReloadUserConfig: reload,
		}))
		if response.Error != nil {
			t.Fatalf("config/batchWrite error = %+v", response.Error)
		}
	}
	seedSkillsCache := func() {
		t.Helper()
		skills.mu.Lock()
		skills.cache = map[string]skillsCacheEntry{"seed": {}}
		skills.mu.Unlock()
	}
	skillsCacheSeeded := func() bool {
		skills.mu.Lock()
		defer skills.mu.Unlock()
		return len(skills.cache) > 0
	}
	globalEpoch := func() uint64 {
		router.mcpRuntimes.mu.Lock()
		defer router.mcpRuntimes.mu.Unlock()
		return router.mcpRuntimes.globalEpoch
	}

	// Launch defaults never touch the running configuration, even with reload on.
	seedSkillsCache()
	write([]config.ConfigEdit{{KeyPath: "model", Value: "gpt-5"}}, true)
	if !skillsCacheSeeded() {
		t.Fatal("session-defaults-only write cleared the skills cache")
	}
	if globalEpoch() != 0 {
		t.Fatalf("session-defaults-only write reloaded threads: epoch=%d", globalEpoch())
	}

	// A real configuration change clears the derived caches even without a reload.
	seedSkillsCache()
	write([]config.ConfigEdit{{KeyPath: "tui.theme", Value: "dark"}}, false)
	if skillsCacheSeeded() {
		t.Fatal("config mutation did not clear the skills cache")
	}
	if globalEpoch() != 0 {
		t.Fatalf("reload ran without reloadUserConfig: epoch=%d", globalEpoch())
	}

	// reloadUserConfig refreshes the config-derived runtime state.
	seedSkillsCache()
	write([]config.ConfigEdit{{KeyPath: "tui.theme", Value: "light"}}, true)
	if globalEpoch() == 0 {
		t.Fatal("reloadUserConfig did not refresh the loaded threads")
	}
}
