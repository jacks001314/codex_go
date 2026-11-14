package mcp

import (
	"strings"
	"testing"
)

func environmentSelectorFixture() *MCPService {
	return NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {
			Name: "docs",
			Config: ServerConfig{
				URL:           "https://docs.example/mcp",
				EnvironmentID: "env-a",
				Enabled:       true,
			},
		},
	}})
}

// Rust #61257: an explicit environmentId selects the registration stored under
// that scope, and omitting it keeps the historical name-only behavior.
func TestMCPEnvironmentSelectorFiltersStatusLikeRust(t *testing.T) {
	service := environmentSelectorFixture()
	envA, envB := "env-a", "env-b"

	if got := service.ListStatus(&MCPListServerStatusParams{EnvironmentID: &envA}); len(got.Data) != 1 {
		t.Fatalf("status for env-a = %d entries, want 1", len(got.Data))
	}
	if got := service.ListStatus(&MCPListServerStatusParams{EnvironmentID: &envB}); len(got.Data) != 0 {
		t.Fatalf("status for env-b = %d entries, want 0", len(got.Data))
	}
	if got := service.ListStatus(&MCPListServerStatusParams{}); len(got.Data) != 1 {
		t.Fatalf("status without selector = %d entries, want 1", len(got.Data))
	}
	if got := service.ListStatus(&MCPListServerStatusParams{ServerName: strPtrForEnvTest("docs"), EnvironmentID: &envA}); len(got.Data) != 1 {
		t.Fatalf("name+env selection = %d entries, want 1", len(got.Data))
	}
}

func TestMCPEnvironmentSelectorRejectsUnknownScopeLikeRust(t *testing.T) {
	service := environmentSelectorFixture()
	envA, envB := "env-a", "env-b"

	if _, err := service.CallTool(&MCPToolCallParams{Server: "docs", Tool: "search", EnvironmentID: &envB}); err == nil || !strings.Contains(err.Error(), "unknown MCP server") {
		t.Fatalf("CallTool(env-b) error = %v", err)
	}
	if _, err := service.ReadResource(&MCPResourceReadParams{Server: "docs", URI: "docs://page", EnvironmentID: &envB}); err == nil || !strings.Contains(err.Error(), "unknown MCP server") {
		t.Fatalf("ReadResource(env-b) error = %v", err)
	}
	if _, err := service.OauthLogin(&MCPServerOauthLoginParams{Name: "docs", EnvironmentID: &envB}); err == nil || !strings.Contains(err.Error(), "not registered in environment env-b") {
		t.Fatalf("OauthLogin(env-b) error = %v", err)
	}

	// The matching selector reaches the transport layer instead of being
	// rejected by the scope check.
	if _, err := service.CallTool(&MCPToolCallParams{Server: "docs", Tool: "search", EnvironmentID: &envA}); err != nil && strings.Contains(err.Error(), "unknown MCP server") {
		t.Fatalf("CallTool(env-a) rejected the matching scope: %v", err)
	}
}

func TestMCPEnvironmentSelectorIgnoresEmptyValueLikeRust(t *testing.T) {
	service := environmentSelectorFixture()
	empty := "  "
	if _, err := service.CallTool(&MCPToolCallParams{Server: "docs", Tool: "search", EnvironmentID: &empty}); err != nil && strings.Contains(err.Error(), "unknown MCP server") {
		t.Fatalf("empty selector rejected: %v", err)
	}
}

func strPtrForEnvTest(value string) *string { return &value }
