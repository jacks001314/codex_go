package mcp

import "testing"

// Rust #61257: status reporting carries the registration scope so same-named
// servers in different environments stay distinguishable.
func TestMCPServerStatusCarriesEnvironmentIdentityLikeRust(t *testing.T) {
	service := environmentSelectorFixture()
	response := service.ListStatus(&MCPListServerStatusParams{Detail: nil})
	if len(response.Data) != 1 {
		t.Fatalf("status entries = %d, want 1", len(response.Data))
	}
	status := response.Data[0]
	if status.EnvironmentID == nil || *status.EnvironmentID != "env-a" {
		t.Fatalf("environmentId = %v, want env-a", status.EnvironmentID)
	}

	defaultService := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Name: "docs", Config: ServerConfig{URL: "https://docs.example/mcp", Enabled: true}},
	}})
	defaultStatus := defaultService.ListStatus(&MCPListServerStatusParams{})
	if len(defaultStatus.Data) != 1 || defaultStatus.Data[0].EnvironmentID != nil {
		t.Fatalf("thread-wide status environmentId = %v, want nil", defaultStatus.Data)
	}
}
