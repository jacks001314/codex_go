package mcp

import "strings"

// McpServerAuthChatGPT is Rust's `McpServerAuth::ChatGpt` value for a server's
// `auth` field (codex-rs/codex-mcp binding, #62392).
const McpServerAuthChatGPT = "chatgpt"

// SessionAuthMode names the session credential classes that gate result-body
// capture. Go keeps the auth snapshot on the request, so the caller resolves the
// mode once per turn.
type SessionAuthMode string

const (
	SessionAuthAPIKey            SessionAuthMode = "apikey"
	SessionAuthChatGPT           SessionAuthMode = "chatgpt"
	SessionAuthChatGPTAuthTokens SessionAuthMode = "chatgpt_auth_tokens"
)

// SessionAuthModePermitsResultBody reports whether a session credential class
// may supply Apps result bodies to its own ChatGPT endpoint (Rust AuthMode
// Chatgpt | ChatgptAuthTokens).
func SessionAuthModePermitsResultBody(mode SessionAuthMode) bool {
	return mode == SessionAuthChatGPT || mode == SessionAuthChatGPTAuthTokens
}

// isAppsAttributionHeader reports the only headers a result-body source may
// declare beyond the runtime credential (Rust is_apps_attribution_header).
func isAppsAttributionHeader(name string) bool {
	return strings.EqualFold(name, "x-openai-product-sku") || strings.EqualFold(name, "originator")
}

// PermitsResultBody reports whether a completed call to this server may supply
// its result body to the session (Rust PreparedMcpCall::permits_result_body,
// #62392): the call must go to a streamable-HTTP ChatGPT server whose only extra
// headers are Apps attribution, with no static bearer, header helper or explicit
// Authorization that would bypass the runtime credential, and the session must
// hold a runtime ChatGPT credential for the same backend.
func (c ServerConfig) PermitsResultBody(mode SessionAuthMode, runtimeAuth *RuntimeAuth) bool {
	if !SessionAuthModePermitsResultBody(mode) {
		return false
	}
	if runtimeAuth == nil || !runtimeAuth.UsesCodexBackend {
		return false
	}
	if strings.TrimSpace(c.Command) != "" || strings.TrimSpace(c.URL) == "" {
		return false
	}
	if strings.TrimSpace(c.BearerTokenEnvVar) != "" || strings.TrimSpace(c.HTTPHeadersHelper) != "" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(c.Auth), McpServerAuthChatGPT) {
		return false
	}
	for name := range c.HTTPHeaders {
		if !isAppsAttributionHeader(name) {
			return false
		}
	}
	for name := range c.EnvHTTPHeaders {
		if !isAppsAttributionHeader(name) {
			return false
		}
	}
	return true
}
