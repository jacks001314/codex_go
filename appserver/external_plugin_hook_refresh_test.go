package appserver

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"codex_go/plugin"
	"codex_go/turn"
)

// Mirrors Rust #42990: a loaded session must observe plugin hook changes made
// by another process. Go re-derives hook discovery for every turn, so the
// turn adapter reads the current hooks.json without an explicit notification.
func TestLoadedSessionRefreshesExternallyUpdatedPluginHooksLikeRust(t *testing.T) {
	cwd := t.TempDir()
	pluginRoot := t.TempDir()
	hooksDir := filepath.Join(pluginRoot, "hooks")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	hooksPath := filepath.Join(hooksDir, "hooks.json")
	writeHook := func(version string) {
		t.Helper()
		body := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo ` + version + `"}]}]}}`
		if err := os.WriteFile(hooksPath, []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", version, err)
		}
	}
	writeHook("1.0.0")

	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{
		Summary:         plugin.PluginSummary{Name: "demo", MarketplaceName: "test", Installed: true, Enabled: true},
		Hooks:           []plugin.PluginHookSummary{{Key: "pre-tool", Enabled: true}},
		MarketplaceRoot: pluginRoot,
	})
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:     cwd,
		Plugins:        plugins,
		HooksDiscovery: NewHookDiscoveryService(""),
		HookRunner:     NewHookRunner(),
	})

	currentCommand := func(turnNumber int) string {
		t.Helper()
		adapter, ok := router.turnHookAdapter(
			&turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd},
			"turn-"+strconv.Itoa(turnNumber),
		).(*ToolHookAdapter)
		if !ok || adapter == nil {
			t.Fatalf("turnHookAdapter() = %T, want *ToolHookAdapter", adapter)
		}
		for _, hook := range adapter.Hooks {
			if hook.Source == HookSourcePlugin && hook.Command != nil {
				return *hook.Command
			}
		}
		t.Fatalf("turn adapter hooks = %#v, want a plugin hook", adapter.Hooks)
		return ""
	}

	if got := currentCommand(1); got != "echo 1.0.0" {
		t.Fatalf("first turn command = %q, want echo 1.0.0", got)
	}
	// Another process upgrades the shared plugin without notifying the server.
	writeHook("2.0.0")
	if got := currentCommand(2); got != "echo 2.0.0" {
		t.Fatalf("upgraded turn command = %q, want echo 2.0.0", got)
	}
	// A rollback must be visible on the next turn as well.
	writeHook("0.9.0")
	if got := currentCommand(3); got != "echo 0.9.0" {
		t.Fatalf("rolled-back turn command = %q, want echo 0.9.0", got)
	}
}
