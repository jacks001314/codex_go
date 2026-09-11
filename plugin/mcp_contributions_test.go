package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPluginMCPDeclarationCannotSelectEMAAuth(t *testing.T) {
	root := t.TempDir()
	contents := `{"mcpServers":{` +
		`"enterprise":{"type":"http","url":"https://resource.example/mcp","auth":"ema_auth"},` +
		`"docs":{"type":"http","url":"https://docs.example/mcp"}}}`
	if err := os.WriteFile(filepath.Join(root, "mcp.json"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write mcp.json: %v", err)
	}
	servers := readPluginMCPServerConfigs(root, root)
	if _, ok := servers["enterprise"]; ok {
		t.Fatal("plugin MCP declaration selecting ema_auth must be dropped")
	}
	if _, ok := servers["docs"]; !ok {
		t.Fatal("valid sibling plugin MCP declaration must still load")
	}
}
