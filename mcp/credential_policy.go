package mcp

import (
	"fmt"
	"strings"
)

// CredentialPolicy mirrors Rust #48143's McpCredentialPolicy: authority to
// resolve environment-variable credentials, retained independently of executor
// capabilities so reconnecting to an executor that lacks environment credential
// resolution cannot promote a guest declaration to host access.
type CredentialPolicy string

const (
	// CredentialPolicyHostFallbackAllowed lets host-configured servers resolve
	// local and legacy remote credentials from the host environment (Rust's
	// HostFallbackAllowed).
	CredentialPolicyHostFallbackAllowed CredentialPolicy = "host_fallback_allowed"
	// CredentialPolicyExecutorOnly forbids reading host environment values for an
	// executor-discovered declaration (Rust's ExecutorOnly).
	CredentialPolicyExecutorOnly CredentialPolicy = "executor_only"
)

// EffectiveCredentialPolicy resolves a possibly-unset policy. The zero value is
// host-owned configuration, which keeps every existing host path unchanged.
func EffectiveCredentialPolicy(policy CredentialPolicy) CredentialPolicy {
	if policy == CredentialPolicyExecutorOnly {
		return CredentialPolicyExecutorOnly
	}
	return CredentialPolicyHostFallbackAllowed
}

// SelectedPluginCredentialPolicy mirrors Rust's
// `McpServerRegistration::from_selected_plugin`: HTTP credential authority comes
// from the plugin's selected root, so a streamable HTTP plugin declared by a
// non-default environment may not resolve credentials on the host.
func SelectedPluginCredentialPolicy(sourceEnvironmentID string, config ServerConfig) CredentialPolicy {
	if strings.TrimSpace(sourceEnvironmentID) != strings.TrimSpace(DefaultMCPServerEnvironmentID) &&
		strings.TrimSpace(config.URL) != "" &&
		strings.TrimSpace(config.Command) == "" {
		return CredentialPolicyExecutorOnly
	}
	return CredentialPolicyHostFallbackAllowed
}

// ValidateExecutorDiscoveryCredentialPolicy mirrors Rust's `make_rmcp_client`
// gates for an executor-discovered declaration: it must be a remote HTTP
// transport that needs no host environment headers or helpers, and it may not
// fall back to a host bearer token. The returned message is Rust's.
func ValidateExecutorDiscoveryCredentialPolicy(name string, config *ServerConfig) error {
	if config == nil || EffectiveCredentialPolicy(config.CredentialPolicy) != CredentialPolicyExecutorOnly {
		return nil
	}
	name = strings.TrimSpace(name)
	if config.IsLocalEnvironment() ||
		strings.TrimSpace(config.URL) == "" ||
		strings.TrimSpace(config.Command) != "" ||
		len(config.EnvHTTPHeaders) > 0 ||
		strings.TrimSpace(config.HTTPHeadersHelper) != "" {
		return fmt.Errorf("executor-discovered MCP server '%s' requires a remote HTTP transport without host environment headers or helpers", name)
	}
	if strings.TrimSpace(config.BearerTokenEnvVar) != "" {
		return fmt.Errorf("MCP server '%s' requires executor-side environment credential resolution; update the executor to a version that supports it (host fallback is disabled)", name)
	}
	return nil
}
