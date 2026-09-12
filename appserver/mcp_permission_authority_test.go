package appserver

import (
	"testing"

	"codex_go/mcp"
	"codex_go/sandbox"
)

// Mirrors Rust #40728 wiring: the thread runtime publishes the thread authority
// and resolves every enabled server, so servers stay usable before any
// environment is selected (Go keeps pre-attachment servers available).
func TestApplyMCPPermissionAuthorityPublishesThreadAuthority(t *testing.T) {
	threadProfile := sandbox.ReadOnlyPermissionProfile()
	runtimeConfig := &mcp.RuntimeConfig{
		PermissionProfile: &threadProfile,
		Servers: map[string]mcp.ServerRegistration{
			"codex_apps": {Config: mcp.ServerConfig{URL: "https://apps.example/mcp", Enabled: true}},
			"local":      {Config: mcp.ServerConfig{Command: "mcp-local", Enabled: true}},
			"remote":     {Config: mcp.ServerConfig{URL: "https://remote.example/mcp", EnvironmentID: "remote-1", Enabled: true}},
			"disabled":   {Config: mcp.ServerConfig{Command: "mcp-disabled", Enabled: false}},
		},
	}
	router := &RuntimeRouter{}
	router.applyMCPPermissionAuthority(runtimeConfig, "thread-x", &threadProfile)

	if runtimeConfig.PermissionProfile == nil {
		t.Fatal("thread authority should be published")
	}
	for _, name := range []string{"codex_apps", "local", "remote"} {
		profile, ok := runtimeConfig.PermissionProfileForServer(name)
		if !ok || profile == nil {
			t.Fatalf("%s should have published authority before attachment, got %#v", name, profile)
		}
	}
	if _, ok := runtimeConfig.PermissionProfileForServer("disabled"); ok {
		t.Fatal("disabled server must not have published authority")
	}
}
