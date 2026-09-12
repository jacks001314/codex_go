package appserver

// Rust parity: codex-rs/app-server/src/request_processors/config_processor.rs.
// A config/batchWrite that touches anything beyond the per-session launch
// defaults mutates the running configuration: the derived plugin/skill caches
// are cleared, and with reloadUserConfig the config-derived runtime state of every
// loaded thread is refreshed.

import (
	"strings"

	"codex_go/config"
)

// sessionDefaultsOnlyKeyPaths mirrors Rust's session_defaults_only check: these
// keys only change the session's launch defaults, so a write containing nothing
// else leaves the running configuration untouched.
var sessionDefaultsOnlyKeyPaths = map[string]bool{
	"model":                      true,
	"model_reasoning_effort":     true,
	"plan_mode_reasoning_effort": true,
	"service_tier":               true,
	"personality":                true,
}

func configEditsAreSessionDefaultsOnly(edits []config.ConfigEdit) bool {
	if len(edits) == 0 {
		return false
	}
	for _, edit := range edits {
		if !sessionDefaultsOnlyKeyPaths[strings.TrimSpace(edit.KeyPath)] {
			return false
		}
	}
	return true
}

// applyConfigBatchWriteMutation mirrors Rust's post-write handling in
// config/batchWrite: handle_config_mutation always runs for a non-session-defaults
// write, and reload_user_config additionally rebuilds the user config and
// refreshes the loaded threads' config-derived runtime state. Errors are never
// surfaced to the caller (Rust logs a warning and keeps serving).
func (r *RuntimeRouter) applyConfigBatchWriteMutation(params *config.ConfigBatchWriteParams) {
	if r == nil || params == nil {
		return
	}
	if configEditsAreSessionDefaultsOnly(params.Edits) {
		return
	}
	r.clearConfigDerivedCaches()
	if params.ReloadUserConfig {
		r.reloadUserConfigForLoadedThreads()
	}
}

// clearConfigDerivedCaches mirrors Rust handle_config_mutation
// (plugins_manager().clear_cache() / skills_service().clear_cache()).
func (r *RuntimeRouter) clearConfigDerivedCaches() {
	if r == nil {
		return
	}
	r.clearRecommendedPluginsCache()
	if r.services.Skills != nil {
		r.services.Skills.ClearCache()
	}
}

// reloadUserConfigForLoadedThreads mirrors Rust reload_user_config: rebuild the
// user config and refresh the runtime state every loaded thread derives from it.
// Go resolves most thread state per request, so the refresh covers the derived
// caches and the per-thread MCP runtimes.
func (r *RuntimeRouter) reloadUserConfigForLoadedThreads() {
	if r == nil || r.services.Config == nil {
		return
	}
	if _, err := r.services.Config.Read(&config.ConfigReadParams{}); err != nil {
		// Rust warns and keeps the previous runtime configuration.
		return
	}
	r.configureMCPFromConfig()
	r.mcpRuntimes.invalidateAll()
}

// refreshPluginCachesAndMCPRuntimes mirrors Rust's
// spawn_effective_plugins_changed_task (account login/logout/session switches):
// the plugin/skill caches are cleared and the loaded threads' MCP runtimes are
// invalidated so the next use rebuilds them from the new credentials.
func (r *RuntimeRouter) refreshPluginCachesAndMCPRuntimes() {
	if r == nil {
		return
	}
	r.clearConfigDerivedCaches()
	r.mcpRuntimes.invalidateAll()
}
