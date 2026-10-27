package mcp

import (
	"strings"
)

// MCPNetworkPolicyMetaCapability is the experimental client/server capability
// for call-scoped network policy metadata (Rust #56278).
const MCPNetworkPolicyMetaCapability = "codex/network-policy-meta"

// mcpClientFeatures lists the optional capabilities one MCP connection
// advertises. Rust projects privileged capabilities per server in
// client_capabilities::server_mcp_extensions, so a caller-supplied capability
// never grants one.
type mcpClientFeatures struct {
	OpenAIForm       bool
	AuthChange       bool
	UserVerification bool
	EMAAuth          bool
	NetworkPolicy    bool
}

// mcpIsTrustedChatGPTMCPServer ports Rust is_trusted_chatgpt_mcp_server: only a
// streamable HTTP server on the configured ChatGPT origin (or a known ChatGPT
// origin) may receive network policy metadata.
func mcpIsTrustedChatGPTMCPServer(config *ServerConfig, chatGPTBaseURL string) bool {
	if config == nil || strings.TrimSpace(config.URL) == "" {
		return false
	}
	return trustedChatGPTMCPOrigin(config.URL, chatGPTBaseURL)
}
