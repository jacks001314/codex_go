package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/session"
)

// Mirrors Rust #60730: an MCP refresh reapplies the session's plugin and app
// overrides instead of rebuilding a layer stack that silently drops them. Go
// stores those overrides on the thread record, so the refresh must leave them
// available to the thread's next configuration projection.
func TestMCPConfigRefreshPreservesSessionAppAndPluginOverridesLikeRust(t *testing.T) {
	home := t.TempDir()
	writeMCPConfigRefreshTestConfig(t, home, "")
	router := newMCPConfigRefreshTestRouter(home)
	record := &session.Record{Metadata: session.Metadata{Extra: map[string]any{"config": map[string]any{
		"plugins": map[string]any{"docs@market": map[string]any{"enabled": true}},
		"apps":    map[string]any{"calendar": map[string]any{"enabled": true}},
	}}}}
	if err := router.reloadMCPConfigStrict(); err != nil {
		t.Fatalf("reload MCP config error = %v", err)
	}
	overrides := threadRecordConfigOverrides(record)
	if overrides["plugins"] == nil || overrides["apps"] == nil {
		t.Fatalf("session overrides after refresh = %#v", overrides)
	}
	cfg := &config.Config{Values: map[string]any{}}
	applyRuntimeConfigOverrides(cfg, mergeTurnConfigOverrides(overrides, nil))
	if cfg.Values["plugins"] == nil || cfg.Values["apps"] == nil {
		t.Fatalf("projected session values after refresh = %#v", cfg.Values)
	}
}
