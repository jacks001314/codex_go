package appserver

// Rust parity: codex-rs/app-server/src/mcp_refresh.rs and
// codex-rs/app-server/src/request_processors/config_processor.rs (#49260).
//
// A configuration reload must honor the current managed restrictions: a reload
// that cannot load or resolve the refreshed configuration must not leave the
// running sessions holding the enterprise (EMA) MCP authority they had before
// it. Go keeps one process-wide MCP runtime plus per-thread runtimes that are
// rebuilt lazily from the current configuration, so a failed reload republishes
// the last applied configuration with every enterprise server disabled and
// invalidates the loaded threads' runtimes so their next use rebuilds from that
// fail-closed state.
//
// Two Rust mechanisms have no Go counterpart and are intentionally not ported:
//
//   - ConfigRefreshOutcome::Stale and the bounded 3x3 retry loop of
//     reload_mcp_config_with_policy. Go publishes the runtime MCP configuration
//     inside one synchronous critical section (configureMCPFromConfigChecked has
//     no await point) and rebuilds each thread runtime from the current config
//     fingerprint on next use (mcpRuntimeCoordinator.serviceForThread), so no
//     captured snapshot can be superseded between its capture and publication.
//   - The per-session enterprise registration retained until a new session is
//     constructed (Session::disable_mcp_enterprise_auth clears only that
//     session's own configuration). Go re-resolves the enterprise profile from
//     the current configuration on every read, so the process-wide fail-closed
//     publication is its equivalent.

import (
	"log/slog"

	"codex_go/mcp"
)

// mcpReloadPolicy mirrors Rust ReloadPolicy: config/mcpServer/reload runs the
// strict reload and reports a failure to its caller, while the reload that
// follows a configuration mutation is best-effort and only logs it.
type mcpReloadPolicy int

const (
	mcpReloadStrict mcpReloadPolicy = iota
	mcpReloadBestEffort
)

// reloadMCPConfigStrict mirrors Rust reload_mcp_config.
func (r *RuntimeRouter) reloadMCPConfigStrict() error {
	return r.reloadMCPConfigWithPolicy(mcpReloadStrict)
}

// reloadMCPConfigBestEffort mirrors Rust reload_mcp_config_best_effort.
func (r *RuntimeRouter) reloadMCPConfigBestEffort() {
	if err := r.reloadMCPConfigWithPolicy(mcpReloadBestEffort); err != nil {
		slog.Warn("failed to reload MCP configuration", "error", err)
	}
}

func (r *RuntimeRouter) reloadMCPConfigWithPolicy(policy mcpReloadPolicy) error {
	if r == nil || r.services.Config == nil {
		return nil
	}
	reloadErr := r.configureMCPFromConfigChecked()
	if reloadErr == nil {
		return nil
	}
	// Rust finishes publishing the fail-closed configuration
	// (disabled_enterprise_config / disable_mcp_enterprise_auth) for every
	// loaded session before it reports the failure, so a failed reload never
	// keeps enterprise MCP authority running.
	r.disableMCPEnterpriseAuthForLoadedThreads()
	if policy == mcpReloadStrict {
		return reloadErr
	}
	slog.Warn("failed to reload MCP configuration", "error", reloadErr)
	return nil
}

// disableMCPEnterpriseAuthForLoadedThreads mirrors Rust's fail-closed
// publication after a rejected or unloadable refresh: the configuration that was
// last applied is republished with every enterprise (EMA) MCP server disabled,
// the loaded threads' MCP runtimes are invalidated so their next use rebuilds
// from it, and a new session is required to restore enterprise authority.
func (r *RuntimeRouter) disableMCPEnterpriseAuthForLoadedThreads() {
	if r == nil || r.services.Config == nil {
		return
	}
	failClosed := r.appliedMCPRuntimeConfigWithoutEnterpriseAuth()
	if failClosed == nil {
		return
	}
	r.requireMCP().ApplyRuntimeConfig(failClosed)
	r.mcpConfigManaged.Store(true)
	if r.mcpRuntimes != nil {
		r.mcpRuntimes.invalidateAll()
	}
	r.prewarmLoadedMCPThreads()
}

// appliedMCPRuntimeConfigWithoutEnterpriseAuth clones the last applied runtime
// MCP configuration and disables its enterprise servers, mirroring
// Config::disable_mcp_enterprise_auth. The enterprise profile itself is
// re-resolved from configuration on every read, so Go has nothing else to clear.
func (r *RuntimeRouter) appliedMCPRuntimeConfigWithoutEnterpriseAuth() *mcp.RuntimeConfig {
	if r == nil {
		return nil
	}
	r.mcpConfigMu.Lock()
	applied := cloneRuntimeConfigForFailClosed(r.mcpAppliedRuntimeConfig)
	r.mcpConfigMu.Unlock()
	if applied == nil {
		return nil
	}
	for name, registration := range applied.Servers {
		if registration.Config.EffectiveAuth() != mcp.ServerAuthEMAAuth {
			continue
		}
		registration.Config.Enabled = false
		applied.Servers[name] = registration
	}
	return applied
}

// rememberAppliedMCPRuntimeConfig records the configuration that was just
// published so a later failed refresh can republish it fail-closed.
func (r *RuntimeRouter) rememberAppliedMCPRuntimeConfig(runtime *mcp.RuntimeConfig) {
	if r == nil {
		return
	}
	r.mcpConfigMu.Lock()
	r.mcpAppliedRuntimeConfig = cloneRuntimeConfigForFailClosed(runtime)
	r.mcpConfigMu.Unlock()
}

// cloneRuntimeConfigForFailClosed copies a runtime MCP configuration so the
// fail-closed path can disable servers without mutating the published snapshot.
// Only server registration fields are replaced, so a shallow copy of each
// registration is enough.
func cloneRuntimeConfigForFailClosed(runtime *mcp.RuntimeConfig) *mcp.RuntimeConfig {
	if runtime == nil {
		return nil
	}
	cloned := *runtime
	if runtime.Servers == nil {
		return &cloned
	}
	cloned.Servers = make(map[string]mcp.ServerRegistration, len(runtime.Servers))
	for name, registration := range runtime.Servers {
		cloned.Servers[name] = registration
	}
	return &cloned
}
