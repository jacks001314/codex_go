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
// Enterprise authority is admitted, not re-derived: `admitEnterpriseMCPAuthority`
// mirrors Config::to_mcp_config_with_loaded_plugins and the `enterprise_retired`
// predicate of Config::resolve_runtime_refresh. Enterprise (EMA) servers stay
// enabled only while `use_xaa` is on and the configuration still declares the
// trusted profile and the exact registration the runtime was admitted with; a
// refresh that removes or replaces either one disables enterprise MCP instead of
// granting the running sessions new authority. Because Go keeps a single
// process-wide configuration rather than a per-session snapshot, the admitted
// registration recorded here is the process's session boundary: a changed
// enterprise registration is picked up by a new app-server process, the way Rust
// requires a new session.
//
// Two Rust mechanisms have no Go counterpart and are intentionally not ported:
//
//   - ConfigRefreshOutcome::Stale and the bounded 3x3 retry loop of
//     reload_mcp_config_with_policy. Go publishes the runtime MCP configuration
//     inside one synchronous critical section (configureMCPFromConfigChecked has
//     no await point) and rebuilds each thread runtime from the current config
//     fingerprint on next use (mcpRuntimeCoordinator.serviceForThread), so no
//     captured snapshot can be superseded between its capture and publication.
//   - ManagedFeatures::refresh_mcp_features / ConfigLayerStack::
//     with_mcp_requirements_from. Go resolves feature settings and managed
//     requirements from the current configuration on every read
//     (Config::FeatureSettingsWithLegacyUsages, ConfigService.Requirements), so
//     nothing is cached to refresh.

import (
	"encoding/json"
	"log/slog"

	"codex_go/config"
	"codex_go/features"
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
	applied := r.mcpAppliedRuntimeConfig
	r.mcpConfigMu.Unlock()
	return mcpRuntimeConfigWithoutEnterpriseAuth(applied)
}

// rememberAppliedMCPRuntimeConfig records the configuration that was just
// published so a later failed refresh can republish it fail-closed. The
// enterprise admission only advances when the publication actually carries
// enterprise authority: a fail-closed or retired publication must not erase the
// registration the running sessions were admitted with, because Rust #49260
// keeps the session's admitted registrations and requires a new session before
// enterprise authority can be restored from a different registration.
func (r *RuntimeRouter) rememberAppliedMCPRuntimeConfig(runtime *mcp.RuntimeConfig, enterpriseProfileFingerprint string) {
	if r == nil {
		return
	}
	admission := mcpEnterpriseAdmission{
		profileFingerprint: enterpriseProfileFingerprint,
		servers:            map[string]string{},
	}
	if runtime != nil {
		for name, registration := range runtime.Servers {
			if registration.Config.EffectiveAuth() != mcp.ServerAuthEMAAuth || !registration.Config.Enabled {
				continue
			}
			admission.servers[name] = enterpriseServerRegistrationFingerprint(registration.Config)
		}
	}
	r.mcpConfigMu.Lock()
	r.mcpAppliedRuntimeConfig = cloneRuntimeConfigForFailClosed(runtime)
	if len(admission.servers) > 0 {
		r.mcpEnterpriseAdmission = admission
	}
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

// mcpEnterpriseAdmission records the enterprise (EMA) MCP authority the runtime
// last published: the trusted profile the servers were admitted under, and the
// registration fingerprint of every enterprise server that was admitted enabled.
type mcpEnterpriseAdmission struct {
	profileFingerprint string
	servers            map[string]string
}

// admitEnterpriseMCPAuthority mirrors the Rust #49260 enterprise admission rules
// (Config::to_mcp_config_with_loaded_plugins only enables EMA when `use_xaa` is
// on and a trusted profile is declared; Config::resolve_runtime_refresh retires
// enterprise MCP when the profile is missing or replaced, when an admitted
// server disappears or is no longer enabled, or when its registration changes).
// It returns the configuration to publish together with the profile fingerprint
// the caller records as the running admission, and disables every enterprise
// server when the current configuration no longer admits the same authority.
func (r *RuntimeRouter) admitEnterpriseMCPAuthority(values map[string]any, featureSettings map[string]bool, next *mcp.RuntimeConfig) (*mcp.RuntimeConfig, string) {
	if r == nil || next == nil {
		return next, ""
	}
	profileFingerprint := ""
	if features.Enabled(featureSettings, config.XAAFeatureKey()) {
		profileFingerprint = config.MCPEnterpriseManagedAuthFingerprint(values["mcp_enterprise_managed_auth"])
	}
	if profileFingerprint == "" {
		// No trusted profile (or `use_xaa` is off): the catalog cannot admit an
		// enterprise registration, so an enterprise server must not stay enabled.
		return mcpRuntimeConfigWithoutEnterpriseAuth(next), ""
	}
	admitted := r.enterpriseAdmissionSnapshot()
	if len(admitted.servers) == 0 {
		return next, profileFingerprint
	}
	if admitted.profileFingerprint != profileFingerprint {
		// The trusted profile was replaced; a running session must not adopt new
		// enterprise authority.
		return mcpRuntimeConfigWithoutEnterpriseAuth(next), ""
	}
	for name, fingerprint := range admitted.servers {
		registration, ok := next.Servers[name]
		if !ok || !registration.Config.Enabled ||
			registration.Config.EffectiveAuth() != mcp.ServerAuthEMAAuth ||
			enterpriseServerRegistrationFingerprint(registration.Config) != fingerprint {
			return mcpRuntimeConfigWithoutEnterpriseAuth(next), ""
		}
	}
	return next, profileFingerprint
}

func (r *RuntimeRouter) enterpriseAdmissionSnapshot() mcpEnterpriseAdmission {
	if r == nil {
		return mcpEnterpriseAdmission{}
	}
	r.mcpConfigMu.Lock()
	defer r.mcpConfigMu.Unlock()
	return r.mcpEnterpriseAdmission
}

// mcpRuntimeConfigWithoutEnterpriseAuth clones a runtime MCP configuration with
// every enterprise (EMA) server disabled, mirroring
// Config::disable_mcp_enterprise_auth.
func mcpRuntimeConfigWithoutEnterpriseAuth(runtime *mcp.RuntimeConfig) *mcp.RuntimeConfig {
	cloned := cloneRuntimeConfigForFailClosed(runtime)
	if cloned == nil {
		return nil
	}
	for name, registration := range cloned.Servers {
		if registration.Config.EffectiveAuth() != mcp.ServerAuthEMAAuth {
			continue
		}
		registration.Config.Enabled = false
		cloned.Servers[name] = registration
	}
	return cloned
}

// enterpriseServerRegistrationFingerprint covers the registration fields Rust
// #49260 compares when it decides whether a refresh still matches the enterprise
// registration a session was admitted with: enabled, auth, transport, OAuth
// resource and client registration, scopes and the target environment.
func enterpriseServerRegistrationFingerprint(config mcp.ServerConfig) string {
	payload, err := json.Marshal(struct {
		Enabled                        bool              `json:"enabled"`
		Auth                           string            `json:"auth"`
		Command                        string            `json:"command"`
		Args                           []string          `json:"args"`
		Env                            map[string]string `json:"env"`
		EnvVars                        []mcp.EnvVar      `json:"env_vars"`
		CWD                            string            `json:"cwd"`
		URL                            string            `json:"url"`
		BearerTokenEnvVar              string            `json:"bearer_token_env_var"`
		HTTPHeaders                    map[string]string `json:"http_headers"`
		EnvHTTPHeaders                 map[string]string `json:"env_http_headers"`
		HTTPHeadersHelper              string            `json:"http_headers_helper"`
		OAuthClientID                  string            `json:"oauth_client_id"`
		OAuthCallbackPort              uint16            `json:"oauth_callback_port"`
		OAuthCallbackURL               string            `json:"oauth_callback_url"`
		OAuthAuthorizationServerIssuer string            `json:"oauth_authorization_server_issuer"`
		OAuthResource                  string            `json:"oauth_resource"`
		Scopes                         []string          `json:"scopes"`
		EnvironmentID                  string            `json:"environment_id"`
	}{
		Enabled:                        config.Enabled,
		Auth:                           config.Auth,
		Command:                        config.Command,
		Args:                           config.Args,
		Env:                            config.Env,
		EnvVars:                        config.EnvVars,
		CWD:                            config.CWD,
		URL:                            config.URL,
		BearerTokenEnvVar:              config.BearerTokenEnvVar,
		HTTPHeaders:                    config.HTTPHeaders,
		EnvHTTPHeaders:                 config.EnvHTTPHeaders,
		HTTPHeadersHelper:              config.HTTPHeadersHelper,
		OAuthClientID:                  config.OAuthClientID,
		OAuthCallbackPort:              config.OAuthCallbackPort,
		OAuthCallbackURL:               config.OAuthCallbackURL,
		OAuthAuthorizationServerIssuer: config.OAuthAuthorizationServerIssuer,
		OAuthResource:                  config.OAuthResource,
		Scopes:                         config.Scopes,
		EnvironmentID:                  config.EnvironmentID,
	})
	if err != nil {
		return ""
	}
	return string(payload)
}
