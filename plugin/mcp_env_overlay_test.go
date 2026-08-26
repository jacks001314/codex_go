package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

// TestApplyCodexEnvOverlayForwardsOnlyMatchingStdioEnvVars pins the #40363
// behavior: local env_vars from .codex-plugin/plugin.json are applied to matching
// stdio servers, `${NAME}` references for declared env vars are replaced with host
// forwarding, and remote-sourced env vars are ignored.
func TestApplyCodexEnvOverlayForwardsOnlyMatchingStdioEnvVars(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, ".codex-plugin", "plugin.json"), `{"name":"legacy-plugin"}`)
	mustWriteFile(t, filepath.Join(root, ".mcp.json"), `{
  "mcpServers": {
    "shared": {
      "command": "legacy-server",
      "args": ["legacy"],
      "env_vars": [
        {"name": "DB_PASSWORD"},
        {"name": "API_TOKEN"},
        {"name": "REMOTE_ONLY", "source": "remote"}
      ]
    }
  }
}`)
	mustWriteFile(t, filepath.Join(root, "mcp.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "shared": {
      "type": "stdio",
      "command": "portable-server",
      "args": ["portable"],
      "env": {
        "DB_PASSWORD": "${DB_PASSWORD}",
        "API_TOKEN": "portable-token",
        "REMOTE_ONLY": "${REMOTE_ONLY}",
        "UNLISTED": "${UNLISTED}"
      }
    },
    "portable-only": {"type": "stdio", "command": "portable-only"}
  }
}`)

	configs := readPluginMCPServerConfigs(root, root)
	if len(configs) == 0 {
		t.Fatal("expected agent plugin servers to load")
	}
	applyCodexEnvOverlay(root, configs)

	shared, ok := configs["shared"]
	if !ok {
		t.Fatalf("shared server missing: %#v", configs)
	}
	env := configEnvMap(shared)
	if _, present := env["DB_PASSWORD"]; present {
		t.Fatalf("DB_PASSWORD should be forwarded via env_vars, not env: %#v", env)
	}
	if env["API_TOKEN"] != "portable-token" {
		t.Fatalf("API_TOKEN should remain in env: %#v", env)
	}
	envVars := configEnvVars(shared)
	names := map[string]bool{}
	for _, v := range envVars {
		names[configString(v, "name")] = true
	}
	if !names["DB_PASSWORD"] || !names["API_TOKEN"] {
		t.Fatalf("env_vars should include DB_PASSWORD and API_TOKEN: %#v", envVars)
	}
	if names["REMOTE_ONLY"] {
		t.Fatalf("remote-sourced env var should be ignored: %#v", envVars)
	}
}

func mustWriteFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
