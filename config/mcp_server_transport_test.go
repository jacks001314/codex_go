package config

import (
	"os"
	"strings"
	"testing"
)

// TestValidateMCPServerTransportFieldsLikeRust ports Rust's
// RawMcpServerConfig::try_into transport rules: each field may only appear on
// the transport that supports it, and a table with neither a command nor a URL
// is invalid.
func TestValidateMCPServerTransportFieldsLikeRust(t *testing.T) {
	tests := []struct {
		name  string
		table map[string]any
		want  string
	}{
		{
			name:  "stdio server with stdio fields",
			table: map[string]any{"command": "mcp", "args": []any{"--stdio"}, "env": map[string]any{"A": "b"}, "env_vars": []any{"PATH"}, "cwd": "/tmp"},
		},
		{
			name:  "http server with http fields",
			table: map[string]any{"url": "https://example.com/mcp", "bearer_token_env_var": "TOKEN", "http_headers": map[string]any{"X": "y"}, "env_http_headers": map[string]any{"Y": "Z"}, "http_headers_helper": "helper"},
		},
		{
			name:  "explicit null streamable field is unset",
			table: map[string]any{"command": "mcp", "url": nil, "auth": nil},
		},
		{
			name:  "empty streamable list still counts as set",
			table: map[string]any{"command": "mcp", "http_headers": map[string]any{}},
			want:  "http_headers is not supported for stdio",
		},
		{
			name:  "stdio with url",
			table: map[string]any{"command": "mcp", "url": "https://example.com/mcp"},
			want:  "url is not supported for stdio",
		},
		{
			name:  "stdio with bearer token",
			table: map[string]any{"command": "mcp", "bearer_token": "secret"},
			want:  "bearer_token is not supported for stdio",
		},
		{
			name:  "stdio with oauth",
			table: map[string]any{"command": "mcp", "oauth": map[string]any{"client_id": "id"}},
			want:  "oauth is not supported for stdio",
		},
		{
			name:  "http with args",
			table: map[string]any{"url": "https://example.com/mcp", "args": []any{"--stdio"}},
			want:  "args is not supported for streamable_http",
		},
		{
			name:  "http with cwd",
			table: map[string]any{"url": "https://example.com/mcp", "cwd": "/tmp"},
			want:  "cwd is not supported for streamable_http",
		},
		{
			name:  "http with bearer token",
			table: map[string]any{"url": "https://example.com/mcp", "bearer_token": "secret"},
			want:  "bearer_token is not supported for streamable_http",
		},
		{
			name:  "empty http headers helper",
			table: map[string]any{"url": "https://example.com/mcp", "http_headers_helper": "   "},
			want:  "http_headers_helper must not be empty",
		},
		{
			name:  "remote environment with helper",
			table: map[string]any{"url": "https://example.com/mcp", "http_headers_helper": "helper", "environment_id": "remote-host"},
			want:  "http_headers_helper is only supported for local MCP servers",
		},
		{
			name:  "local environment with helper",
			table: map[string]any{"url": "https://example.com/mcp", "http_headers_helper": "helper", "environment_id": "local"},
		},
		{
			name:  "no transport",
			table: map[string]any{"enabled": true},
			want:  "invalid transport",
		},
		{
			name:  "nested tool fields",
			table: map[string]any{"command": "mcp", "tools": map[string]any{"search": map[string]any{"approval_mode": "prompt", "output_token_limit": 10}}},
		},
		{
			name:  "unknown nested tool field",
			table: map[string]any{"command": "mcp", "tools": map[string]any{"search": map[string]any{"approval_modes": "prompt"}}},
			want:  "unknown configuration field `mcp_servers.srv.tools.search.approval_modes`",
		},
		{
			name:  "env vars entries",
			table: map[string]any{"command": "mcp", "env_vars": []any{"PATH", map[string]any{"name": "TOKEN", "source": "remote"}}},
		},
		{
			name:  "unknown env vars field",
			table: map[string]any{"command": "mcp", "env_vars": []any{map[string]any{"name": "TOKEN", "sources": "local"}}},
			want:  "unknown configuration field `mcp_servers.srv.env_vars.sources`",
		},
		{
			name:  "unsupported env vars source",
			table: map[string]any{"command": "mcp", "env_vars": []any{map[string]any{"name": "TOKEN", "source": "vault"}}},
			want:  "unsupported env_vars source `vault`; expected `local` or `remote`",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMCPServerTransportFields(map[string]any{"srv": tt.table})
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("error = %v, want nil", err)
			case tt.want == "":
				return
			case err == nil:
				t.Fatalf("error = nil, want %q", tt.want)
			case !strings.Contains(err.Error(), tt.want):
				t.Fatalf("error = %q, want it to contain %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), "mcp_servers.srv") {
				t.Fatalf("error %q does not name the server", err)
			}
		})
	}
}

// TestLoadRejectsTransportMismatchedMCPServerLikeRust covers the load boundary:
// Rust fails config load when a server table mixes transports.
func TestLoadRejectsTransportMismatchedMCPServerLikeRust(t *testing.T) {
	home := t.TempDir()
	body := "[mcp_servers.docs]\ncommand = \"mcp-docs\"\nurl = \"https://example.com/mcp\"\n"
	if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	if _, err := LoadEffectiveWithOptions(home, nil); err == nil || !strings.Contains(err.Error(), "url is not supported for stdio") {
		t.Fatalf("load error = %v, want the transport mismatch", err)
	}

	valid := "[mcp_servers.docs]\ncommand = \"mcp-docs\"\nargs = [\"--stdio\"]\n[mcp_servers.remote]\nurl = \"https://example.com/mcp\"\nhttp_headers = { \"X-Test\" = \"1\" }\n"
	if err := os.WriteFile(ConfigPath(home), []byte(valid), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	if _, err := LoadEffectiveWithOptions(home, nil); err != nil {
		t.Fatalf("valid config load error = %v", err)
	}
}
