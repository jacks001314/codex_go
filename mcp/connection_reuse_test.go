package mcp

import (
	"testing"
	"time"
)

// Mirrors Rust #40636: a pending startup is replaced when its startup timeout
// changes, while a ready connection is reused regardless.
func TestMCPStdioConnectionReuseHonorsStartupTimeoutLikeRust(t *testing.T) {
	service := &MCPService{}
	config := &ServerConfig{Command: "mcp-fs", StartupTimeout: time.Second}
	pending := service.stdioClientForServer("docs", config)
	if pending == nil {
		t.Fatal("stdioClientForServer() returned nil")
	}

	config.StartupTimeout = 2 * time.Second
	if replaced := service.stdioClientForServer("docs", config); replaced == pending {
		t.Fatal("pending startup should be replaced when its startup timeout changes")
	}

	// Mark the cached connection ready; the startup budget no longer applies.
	service.mu.Lock()
	ready := service.stdioClients["docs"].client
	service.mu.Unlock()
	ready.mu.Lock()
	ready.initialized = true
	ready.mu.Unlock()

	config.StartupTimeout = 3 * time.Second
	if reused := service.stdioClientForServer("docs", config); reused != ready {
		t.Fatal("ready connection should be reused across a startup timeout change")
	}
}

// Mirrors Rust #40634: changing the configured auth mode replaces the
// connection instead of reusing it.
func TestMCPConnectionCacheKeyIncludesAuthModeLikeRust(t *testing.T) {
	base := &ServerConfig{Command: "mcp-fs", Auth: ServerAuthOAuth}
	oauthKey := mcpConnectionCacheKey(base, false)
	base.Auth = ServerAuthChatGPT
	if chatGPTKey := mcpConnectionCacheKey(base, false); chatGPTKey == oauthKey {
		t.Fatal("auth mode must be part of the MCP connection identity")
	}
}
