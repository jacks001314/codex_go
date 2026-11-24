package historycell

import (
	"strings"
	"testing"
)

// Mirrors Rust #63619 /history_cell/mcp.rs: the verbose inventory shows the
// transport but masks literal env and header values, and names the variable
// behind each env HTTP header.
func TestMCPInventoryMasksEnvAndHeaderValuesLikeRust(t *testing.T) {
	cell := NewMCPToolsOutputFromStatuses([]McpServerStatus{
		{
			Name:           "docs",
			Auth:           "Unsupported",
			Tools:          []string{"list"},
			Command:        "docs-server",
			Env:            []string{"APP_TOKEN", "TOKEN"},
			EnvHTTPHeaders: nil,
		},
		{
			Name:        "http",
			Auth:        "Unsupported",
			Tools:       []string{"ping"},
			URL:         "https://example.com/mcp",
			HTTPHeaders: []string{"Authorization"},
			EnvHTTPHeaders: map[string]string{
				"X-API-Key": "API_KEY_ENV",
			},
		},
	}, true)
	rendered := strings.Join(cell.Lines, "\n")
	for _, want := range []string{
		"  • docs",
		"    • Status: enabled",
		"    • Auth: Unsupported",
		"    • Command: docs-server",
		"    • Env: APP_TOKEN=*****, TOKEN=*****",
		"    • Tools: list",
		"  • http",
		"    • URL: https://example.com/mcp",
		"    • HTTP headers: Authorization=*****",
		"    • Env HTTP headers: X-API-Key=API_KEY_ENV",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("inventory missing %q in:\n%s", want, rendered)
		}
	}
}

// A compact inventory keeps the original one-line summary and never renders
// transport details.
func TestMCPInventoryCompactOmitsTransportLikeRust(t *testing.T) {
	cell := NewMCPToolsOutputFromStatuses([]McpServerStatus{{
		Name:        "http",
		URL:         "https://example.com/mcp",
		HTTPHeaders: []string{"Authorization"},
		Tools:       []string{"ping"},
	}}, false)
	rendered := strings.Join(cell.Lines, "\n")
	if !strings.Contains(rendered, "  • http: ") {
		t.Fatalf("compact inventory missing summary in:\n%s", rendered)
	}
	for _, forbidden := range []string{"HTTP headers", "Authorization", "https://example.com/mcp"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("compact inventory leaked %q in:\n%s", forbidden, rendered)
		}
	}
}

// A disabled server reports the reason and nothing about its transport, so an
// inactive secret-bearing configuration never reaches the screen.
func TestMCPInventoryDisabledServerShowsReasonOnlyLikeRust(t *testing.T) {
	cell := NewMCPToolsOutputFromStatuses([]McpServerStatus{{
		Name:           "docs",
		Disabled:       true,
		DisabledReason: "missing credentials",
		Command:        "docs-server",
		Env:            []string{"TOKEN"},
	}}, true)
	rendered := strings.Join(cell.Lines, "\n")
	if !strings.Contains(rendered, "  • docs (disabled)") {
		t.Fatalf("disabled header missing in:\n%s", rendered)
	}
	if !strings.Contains(rendered, "    • Reason: missing credentials") {
		t.Fatalf("disabled reason missing in:\n%s", rendered)
	}
	for _, forbidden := range []string{"Command:", "Env:", "TOKEN"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("disabled inventory leaked %q in:\n%s", forbidden, rendered)
		}
	}
}
