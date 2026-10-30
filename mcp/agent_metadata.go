package mcp

import (
	"maps"
	"slices"
	"strings"
)

// AgentMCPMetadataRecipient mirrors Rust's AgentMcpMetadataRecipient (#58378).
// Installing one selects the single host plugin registration that receives
// `x-codex-root-turn-id` and `x-codex-cyber-access-program` in every tool-call
// `_meta`. Only a registration from the host plugin manager in the `local` MCP
// environment can match: the plugin id, installed root, server name, and loaded
// transport configuration must all equal the effective registration.
//
// Transport may contain credentials and must not be logged.
type AgentMCPMetadataRecipient struct {
	PluginID   string
	PluginRoot string
	ServerName string
	Transport  ServerConfig
}

// Matches reports whether registration is exactly the host-selected plugin
// registration. Same-name user overrides, thread-selected plugins, and
// extension registrations never match because their source is not Plugin.
func (r *AgentMCPMetadataRecipient) Matches(serverName string, registration ResolvedServer) bool {
	if r == nil || serverName != r.ServerName {
		return false
	}
	if !registration.Config.IsLocalEnvironment() {
		return false
	}
	if !mcpServerTransportEqual(registration.Config, r.Transport) {
		return false
	}
	attribution := registration.PluginAttribution
	return registration.Source == CatalogSourcePlugin &&
		attribution != nil &&
		attribution.PluginID == r.PluginID &&
		strings.TrimSpace(attribution.HostRoot) != "" &&
		attribution.HostRoot == r.PluginRoot
}

// mcpServerTransportEqual compares only the transport-defining fields, the Go
// equivalent of comparing Rust's McpServerTransportConfig enum value. Runtime
// policy fields (credentials, protocol mode, clients) are not transport.
func mcpServerTransportEqual(a ServerConfig, b ServerConfig) bool {
	if a.Command != b.Command || a.CWD != b.CWD || a.URL != b.URL || a.BearerTokenEnvVar != b.BearerTokenEnvVar {
		return false
	}
	if !slices.Equal(a.Args, b.Args) || !slices.Equal(a.EnvVars, b.EnvVars) {
		return false
	}
	return maps.Equal(a.Env, b.Env) &&
		maps.Equal(a.HTTPHeaders, b.HTTPHeaders) &&
		maps.Equal(a.EnvHTTPHeaders, b.EnvHTTPHeaders)
}
