package app

import (
	"strings"
	"testing"

	"codex_go/mcp"
	"codex_go/tui/history_cell"
)

// Mirrors Rust #63619: the TUI inventory reads the effective transport from the
// MCP configuration and hands the history cell only variable and header names,
// so a configured secret cannot reach the screen.
func TestInteractiveMCPInventoryMasksConfiguredSecretsLikeRust(t *testing.T) {
	service := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		"docs": {
			Name: "docs",
			Config: mcp.ServerConfig{
				Command: "docs-server",
				Args:    []string{"--stdio"},
				Env:     map[string]string{"TOKEN": "super-secret-token"},
				Enabled: true,
			},
		},
		"http": {
			Name: "http",
			Config: mcp.ServerConfig{
				URL:            "https://example.com/mcp",
				HTTPHeaders:    map[string]string{"Authorization": "Bearer super-secret-header"},
				EnvHTTPHeaders: map[string]string{"X-API-Key": "API_KEY_ENV"},
				Enabled:        true,
			},
		},
	}})
	statuses := interactiveHistoryMCPStatuses(service.ConfiguredStatuses(), service)
	rendered := strings.Join(historycell.NewMCPToolsOutputFromStatuses(statuses, true).Lines, "\n")
	for _, want := range []string{
		"    • Command: docs-server --stdio",
		"    • Env: TOKEN=*****",
		"    • URL: https://example.com/mcp",
		"    • HTTP headers: Authorization=*****",
		"    • Env HTTP headers: X-API-Key=API_KEY_ENV",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("inventory missing %q in:\n%s", want, rendered)
		}
	}
	for _, secret := range []string{"super-secret-token", "super-secret-header"} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("inventory leaked %q in:\n%s", secret, rendered)
		}
	}
}
