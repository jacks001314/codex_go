package app

import (
	"os"
	"testing"

	"codex_go/mcp"
)

// TestMain pins the MCP OAuth keyring backend to unavailable for the app
// package's tests: this build has a durable keyring on Windows (the Windows
// Credential Manager), and these tests exercise the credentials-file / auto
// fallback flows end to end. Writing real credentials during tests would leak
// entries into the developer's credential store. The keyring path is covered by
// the mcp package's tests with an injected fake store.
func TestMain(m *testing.M) {
	mcp.MCPOAuthKeyringAvailable = false
	os.Exit(m.Run())
}
