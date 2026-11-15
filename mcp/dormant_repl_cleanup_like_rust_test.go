package mcp

import (
	"testing"
	"time"
)

func dormantReplService(config ServerConfig, registration ServerRegistration) *MCPService {
	registration.Config = config
	return NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{"node_repl": registration}})
}

// TestIsDormantPrivateStdioLikeRust mirrors Rust #61437: only a configured,
// local, unshared legacy stdio server whose startup was never requested may
// skip bundled cleanup startup.
func TestIsDormantPrivateStdioLikeRust(t *testing.T) {
	stdio := ServerConfig{Command: "test-helper", Enabled: true, StartupTimeout: time.Second}
	modern := MCPProtocol20260728

	cases := []struct {
		name         string
		config       ServerConfig
		registration ServerRegistration
		want         bool
	}{
		{name: "configured local stdio", config: stdio, want: true},
		{name: "remote environment", config: ServerConfig{Command: "test-helper", Enabled: true, EnvironmentID: "remote"}, want: false},
		{name: "http transport", config: ServerConfig{URL: "https://example.com/mcp", Enabled: true}, want: false},
		{name: "plugin contribution", config: stdio, registration: ServerRegistration{Source: "plugin", PluginID: "other@example"}, want: false},
		{name: "modern protocol", config: ServerConfig{Command: "test-helper", Enabled: true, ProtocolModeOverride: &modern}, want: false},
		{name: "disabled registration", config: ServerConfig{Command: "test-helper", Enabled: false}, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service := dormantReplService(testCase.config, testCase.registration)
			defer service.Close()
			if got := service.IsDormantPrivateStdio("node_repl", ""); got != testCase.want {
				t.Fatalf("IsDormantPrivateStdio() = %v, want %v", got, testCase.want)
			}
		})
	}

	service := dormantReplService(stdio, ServerRegistration{})
	defer service.Close()
	if !service.IsDormantPrivateStdio("node_repl", "") {
		t.Fatal("fresh configured REPL should be dormant")
	}
	service.markServerStartupStarted("node_repl")
	if service.IsDormantPrivateStdio("node_repl", "") {
		t.Fatal("REPL whose startup was requested is no longer dormant")
	}
}
