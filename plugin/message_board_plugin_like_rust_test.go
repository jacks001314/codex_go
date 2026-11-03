package plugin

import (
	"path/filepath"
	"testing"
)

// Rust #58908: the message-board plugin ships connection metadata for the HTTP
// MCP server, defaults to the loopback endpoint, uses the bearer environment
// variable, and marks the server as required.
func TestMessageBoardPluginConnectionMetadataLikeRust(t *testing.T) {
	root := filepath.Join("..", "agentboard", "plugin")
	resolved, err := parsePluginManifestAtRoot(root, newManifestCache())
	if err != nil || resolved == nil {
		t.Fatalf("parsePluginManifestAtRoot() = %#v, %v", resolved, err)
	}
	if resolved.Manifest.Name != "message-board" || resolved.Manifest.Version != "0.1.0" {
		t.Fatalf("manifest = %#v", resolved.Manifest)
	}
	configs := readPluginMCPServerConfigs(root, "")
	server := configs["codex_message_board"]
	if server == nil {
		t.Fatalf("plugin MCP servers = %#v", configs)
	}
	if server["url"] != "http://127.0.0.1:7777/mcp" {
		t.Fatalf("url = %#v", server["url"])
	}
	if server["bearer_token_env_var"] != "CODEX_MESSAGE_BOARD_TOKEN" {
		t.Fatalf("bearer_token_env_var = %#v", server["bearer_token_env_var"])
	}
	if required, ok := server["required"].(bool); !ok || !required {
		t.Fatalf("required = %#v", server["required"])
	}
}
