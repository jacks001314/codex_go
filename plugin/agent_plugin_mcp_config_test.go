package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAgentPluginMCPFile(t *testing.T, root string, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "mcp.json"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseAgentPluginMCPConfigStdioNormalization(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	outcome, err := ParseAgentPluginMCPConfig(`{
		"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
		"mcpServers": {
			"docs": {
				"type": "stdio",
				"command": "./bin/server",
				"args": ["--root", "${PLUGIN_ROOT}", "--data", "${PLUGIN_DATA}"],
				"env": {"TOKEN": "secret"},
				"cwd": "${PLUGIN_ROOT}"
			}
		}
	}`, root, root)
	if err != nil {
		t.Fatalf("ParseAgentPluginMCPConfig() error = %v", err)
	}
	if len(outcome.Errors) != 0 {
		t.Fatalf("errors = %#v", outcome.Errors)
	}
	config, ok := outcome.Servers["docs"]
	if !ok {
		t.Fatalf("servers = %#v", outcome.Servers)
	}
	wantCommand := filepath.Join(root, "bin", "server")
	if config["command"] != wantCommand {
		t.Fatalf("command = %v, want %q", config["command"], wantCommand)
	}
	args := config["args"].([]string)
	if len(args) != 4 || args[1] != root || args[3] != root {
		t.Fatalf("args = %#v", args)
	}
	env := config["env"].(map[string]string)
	if env["TOKEN"] != "secret" || env[pluginRootVariable] != root || env[pluginDataVariable] != root {
		t.Fatalf("env = %#v", env)
	}
	if config["cwd"] != root {
		t.Fatalf("cwd = %v, want %q", config["cwd"], root)
	}
}

func TestParseAgentPluginMCPConfigRejectsUnsafeStdio(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name    string
		server  string
		wantErr string
	}{
		{name: "absolute command", server: `{"type":"stdio","command":"/usr/bin/server"}`, wantErr: "must be a bare executable name"},
		{name: "escaped command", server: `{"type":"stdio","command":"../server"}`, wantErr: "must be a bare executable name"},
		{name: "reserved env", server: `{"type":"stdio","command":"server","env":{"PLUGIN_ROOT":"x"}}`, wantErr: "cannot override reserved variable"},
		{name: "bad cwd", server: `{"type":"stdio","command":"server","cwd":"/etc"}`, wantErr: "`cwd` must be a contained"},
	} {
		t.Run(test.name, func(t *testing.T) {
			outcome, err := ParseAgentPluginMCPConfig(`{
				"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
				"mcpServers": {"docs": `+test.server+`}
			}`, root, root)
			if err != nil {
				t.Fatalf("ParseAgentPluginMCPConfig() error = %v", err)
			}
			if len(outcome.Errors) != 1 || !strings.Contains(outcome.Errors[0].Message, test.wantErr) {
				t.Fatalf("errors = %#v, want containing %q", outcome.Errors, test.wantErr)
			}
			if len(outcome.Servers) != 0 {
				t.Fatalf("servers = %#v, want empty", outcome.Servers)
			}
		})
	}
}

func TestParseAgentPluginMCPConfigHTTPNormalization(t *testing.T) {
	root := t.TempDir()
	outcome, err := ParseAgentPluginMCPConfig(`{
		"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
		"mcpServers": {
			"docs": {
				"type": "streamable-http",
				"url": "https://example.test/mcp",
				"headers": {"x-api-key": "secret", "host": "example.test", "User-Agent": "agent"}
			}
		}
	}`, root, root)
	if err != nil {
		t.Fatalf("ParseAgentPluginMCPConfig() error = %v", err)
	}
	config := outcome.Servers["docs"]
	if config["url"] != "https://example.test/mcp" {
		t.Fatalf("url = %v", config["url"])
	}
	headers := config["http_headers"].(map[string]string)
	if headers["x-api-key"] != "secret" {
		t.Fatalf("headers = %#v", headers)
	}
	for _, owned := range []string{"host", "User-Agent"} {
		if _, exists := headers[owned]; exists {
			t.Fatalf("client-owned header %q was not filtered: %#v", owned, headers)
		}
	}
}

func TestParseAgentPluginMCPConfigValidatesHTTP(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name    string
		server  string
		wantErr string
	}{
		{name: "insecure endpoint", server: `{"type":"streamable-http","url":"http://example.test/mcp"}`, wantErr: "must use HTTPS"},
		{name: "userinfo", server: `{"type":"streamable-http","url":"https://user@example.test/mcp"}`, wantErr: "must not contain user information"},
		{name: "sse", server: `{"type":"sse","url":"https://example.test/sse"}`, wantErr: "SSE transport is not supported"},
		{name: "duplicate headers", server: `{"type":"streamable-http","url":"https://example.test/mcp","headers":{"X-A":"1","x-a":"2"}}`, wantErr: "duplicate case-insensitive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			outcome, err := ParseAgentPluginMCPConfig(`{
				"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
				"mcpServers": {"docs": `+test.server+`}
			}`, root, root)
			if err != nil {
				t.Fatalf("ParseAgentPluginMCPConfig() error = %v", err)
			}
			if len(outcome.Errors) != 1 || !strings.Contains(outcome.Errors[0].Message, test.wantErr) {
				t.Fatalf("errors = %#v, want containing %q", outcome.Errors, test.wantErr)
			}
		})
	}
	// Loopback HTTP is allowed.
	outcome, err := ParseAgentPluginMCPConfig(`{
		"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
		"mcpServers": {"local": {"type":"streamable-http","url":"http://127.0.0.1:8000/mcp"}}
	}`, root, root)
	if err != nil || len(outcome.Errors) != 0 {
		t.Fatalf("loopback outcome = %#v error = %v", outcome, err)
	}
}

func TestParseAgentPluginMCPConfigKeepsValidSiblingServers(t *testing.T) {
	root := t.TempDir()
	outcome, err := ParseAgentPluginMCPConfig(`{
		"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
		"mcpServers": {
			"bad": {"type": "sse", "url": "https://example.test/sse"},
			"good": {"type": "streamable-http", "url": "https://example.test/mcp"}
		}
	}`, root, root)
	if err != nil {
		t.Fatalf("ParseAgentPluginMCPConfig() error = %v", err)
	}
	if len(outcome.Errors) != 1 || outcome.Errors[0].Name != "bad" {
		t.Fatalf("errors = %#v", outcome.Errors)
	}
	if _, ok := outcome.Servers["good"]; !ok {
		t.Fatalf("servers = %#v, want good", outcome.Servers)
	}
}

func TestParseAgentPluginMCPConfigRejectsUnsupportedSchema(t *testing.T) {
	root := t.TempDir()
	_, err := ParseAgentPluginMCPConfig(`{
		"$schema": "https://example.invalid/schema.json",
		"mcpServers": {}
	}`, root, root)
	if !errors.Is(err, ErrAgentPluginMCPUnsupportedSchema) {
		t.Fatalf("error = %v, want ErrAgentPluginMCPUnsupportedSchema", err)
	}
}

func TestReadPluginMCPServerConfigsPrefersAgentPluginSchema(t *testing.T) {
	root := t.TempDir()
	writeAgentPluginMCPFile(t, root, `{
		"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
		"mcpServers": {"docs": {"type":"streamable-http","url":"https://example.test/mcp"}}
	}`)
	configs := readPluginMCPServerConfigs(root, root)
	if len(configs) != 1 {
		t.Fatalf("configs = %#v", configs)
	}
	if configs["docs"]["url"] != "https://example.test/mcp" {
		t.Fatalf("docs = %#v", configs["docs"])
	}
}

func TestReadPluginMCPServerConfigsLegacyMcpJSONStillWorks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"docs":{"type":"http","url":"https://example.test/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	configs := readPluginMCPServerConfigs(root, root)
	if len(configs) != 1 || configs["docs"]["url"] != "https://example.test/mcp" {
		t.Fatalf("configs = %#v", configs)
	}
}
