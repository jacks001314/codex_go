package mcp

import "testing"

func TestRuntimeServerConfigParsesStartupReadinessLikeRust(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value any
		want  MCPStartupReadiness
	}{
		{name: "absent", want: MCPStartupReadinessConnection},
		{name: "connection", key: "startup_readiness", value: "connection", want: MCPStartupReadinessConnection},
		{name: "catalog", key: "startup_readiness", value: "catalog", want: MCPStartupReadinessCatalog},
		{name: "camel case", key: "startupReadiness", value: "catalog", want: MCPStartupReadinessCatalog},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]any{"command": "mcp-docs"}
			if tt.value != nil {
				values[tt.key] = tt.value
			}
			config := runtimeServerConfigFromValues(values)
			if got := config.StartupReadiness.Effective(); got != tt.want {
				t.Fatalf("StartupReadiness.Effective() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMCPStartupReadinessDefaultSerializesOmitted(t *testing.T) {
	config := runtimeServerConfigFromValues(map[string]any{"command": "mcp-docs"})
	key := mcpConnectionCacheKey(config, false)
	catalog := runtimeServerConfigFromValues(map[string]any{
		"command":           "mcp-docs",
		"startup_readiness": "catalog",
	})
	if got := mcpConnectionCacheKey(catalog, false); got != key {
		t.Fatalf("startup readiness changed connection cache identity: default=%q catalog=%q", key, got)
	}
}
