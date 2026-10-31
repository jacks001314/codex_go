package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/mcp"
)

// mcpJSONLikeRustConfig exercises both transports: a stdio server with an empty
// argument and integral timeouts, and a streamable HTTP server that configures a
// header helper, whose value 0.166 reports as "Bearer token" and redacts on read.
const mcpJSONLikeRustConfig = `[mcp_servers.alpha]
command = "node"
args = ["server.js", ""]
startup_timeout_sec = 3
tool_timeout_sec = 4
env_vars = ["TOKEN_A"]
cwd = "/tmp/alpha"

[mcp_servers.alpha.env]
FOO = "bar"

[mcp_servers.beta]
url = "https://mcp.example.test/mcp"
bearer_token_env_var = "BETA_TOKEN"
http_headers_helper = "get-token"

[mcp_servers.beta.env_http_headers]
"X-Env" = "BETA_TOKEN"
`

func writeMCPJSONLikeRustHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(mcpJSONLikeRustConfig), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	return home
}

func runMCPLikeRust(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("%v returned error: %v (stderr %q)", args, err, stderr.String())
	}
	return stdout.String()
}

func requireJSONKeysInOrder(t *testing.T, output string, keys ...string) {
	t.Helper()
	previous := -1
	for _, key := range keys {
		index := strings.Index(output, `"`+key+`":`)
		if index < 0 {
			t.Fatalf("missing key %q in %q", key, output)
		}
		if index <= previous {
			t.Fatalf("key %q is out of order in %q", key, output)
		}
		previous = index
	}
}

func TestMCPGetJSONMatchesRustSerialization(t *testing.T) {
	writeMCPJSONLikeRustHome(t)

	stdioEntry := runMCPLikeRust(t, "mcp", "get", "alpha", "--json")
	requireJSONKeysInOrder(t, stdioEntry,
		"name", "enabled", "disabled_reason", "transport", "enabled_tools",
		"disabled_tools", "startup_timeout_sec", "tool_timeout_sec")
	requireJSONKeysInOrder(t, stdioEntry, "type", "command", "args", "env", "env_vars", "cwd")
	if strings.Contains(stdioEntry, "omit_tools_from") {
		t.Fatalf("mcp get --json still reports omit_tools_from: %q", stdioEntry)
	}
	if !strings.Contains(stdioEntry, `"startup_timeout_sec": 3.0`) ||
		!strings.Contains(stdioEntry, `"tool_timeout_sec": 4.0`) {
		t.Fatalf("timeouts are not rendered as f64 seconds: %q", stdioEntry)
	}
	if !strings.Contains(stdioEntry, "\"args\": [\n      \"server.js\",\n      \"\"\n    ]") {
		t.Fatalf("empty stdio argument was dropped: %q", stdioEntry)
	}

	httpEntry := runMCPLikeRust(t, "mcp", "get", "beta", "--json")
	requireJSONKeysInOrder(t, httpEntry,
		"type", "url", "bearer_token_env_var", "http_headers", "env_http_headers", "http_headers_helper")
	if !strings.Contains(httpEntry, `"http_headers_helper": "<redacted>"`) {
		t.Fatalf("header helper is not redacted like serde: %q", httpEntry)
	}
	if strings.Contains(httpEntry, `\u003c`) {
		t.Fatalf("JSON output HTML-escaped the redaction marker: %q", httpEntry)
	}
	if !strings.Contains(httpEntry, `"http_headers": null`) {
		t.Fatalf("absent headers should be null: %q", httpEntry)
	}
	if !strings.Contains(httpEntry, `"startup_timeout_sec": null`) {
		t.Fatalf("absent timeouts should be null: %q", httpEntry)
	}
}

func TestMCPListJSONMatchesRustSerialization(t *testing.T) {
	writeMCPJSONLikeRustHome(t)

	list := runMCPLikeRust(t, "mcp", "list", "--json")
	requireJSONKeysInOrder(t, list,
		"name", "enabled", "disabled_reason", "transport",
		"startup_timeout_sec", "tool_timeout_sec", "auth_status")
	// 0.166 serializes the core McpAuthStatus enum with rename_all = "snake_case",
	// so the CLI payload says bearer_token while the app-server says bearerToken.
	if !strings.Contains(list, `"auth_status": "unsupported"`) {
		t.Fatalf("stdio server auth status = %q", list)
	}
	if !strings.Contains(list, `"auth_status": "bearer_token"`) {
		t.Fatalf("bearer token env var auth status = %q", list)
	}
	if strings.Contains(list, `"auth_status": "bearerToken"`) {
		t.Fatalf("CLI payload kept the app-server camelCase status: %q", list)
	}
}

func TestMCPListJSONReportsStoredCredentialsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	// gamma carries no bearer token and no helper, so the stored credentials are
	// the only thing that can decide its auth status.
	const serverURL = "https://mcp.example.test/mcp"
	config := `[mcp_servers.gamma]
url = "` + serverURL + `"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	if err := mcp.NewOAuthStore(home).Save(&mcp.OAuthTokenSet{
		ServerName:      "gamma",
		ServerURL:       serverURL,
		ClientID:        "client-1",
		AccessToken:     "access-token",
		ExpiresAtMillis: &expiresAt,
	}); err != nil {
		t.Fatalf("Save OAuth token: %v", err)
	}
	list := runMCPLikeRust(t, "mcp", "list", "--json")
	if !strings.Contains(list, `"auth_status": "o_auth"`) {
		t.Fatalf("stored credentials auth status = %q", list)
	}
	if !strings.Contains(runMCPLikeRust(t, "mcp", "list"), "OAuth") {
		t.Fatalf("text list should print the OAuth status")
	}

	expiredAt := time.Now().Add(-time.Hour).UnixMilli()
	if err := mcp.NewOAuthStore(home).Save(&mcp.OAuthTokenSet{
		ServerName:      "gamma",
		ServerURL:       serverURL,
		ClientID:        "client-1",
		AccessToken:     "access-token",
		ExpiresAtMillis: &expiredAt,
	}); err != nil {
		t.Fatalf("Save expired OAuth token: %v", err)
	}
	list = runMCPLikeRust(t, "mcp", "list", "--json")
	if !strings.Contains(list, `"auth_status": "not_logged_in"`) {
		t.Fatalf("expired credentials auth status = %q", list)
	}
}
