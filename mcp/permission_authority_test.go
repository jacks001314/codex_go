package mcp

import (
	"context"
	"strings"
	"testing"

	"codex_go/sandbox"
	"codex_go/tool"
)

func mcpTestThreadProfile() *sandbox.PermissionProfile {
	profile := sandbox.ReadOnlyPermissionProfile()
	return &profile
}

func mcpTestEnvironmentProfile() *sandbox.PermissionProfile {
	profile := sandbox.FullAccessPermissionProfile()
	return &profile
}

// Mirrors Rust set_server_permission_profiles (#40728): the Apps server, local
// servers, and selected-plugin servers use the thread authority, attached
// environments use their own published authority, and anything unresolved gets
// no entry.
func TestMCPServerPermissionProfilesResolutionLikeRust(t *testing.T) {
	runtimeConfig := &RuntimeConfig{
		PermissionProfile:    mcpTestThreadProfile(),
		AvailableEnvironment: []string{"remote-1"},
		Servers: map[string]ServerRegistration{
			"codex_apps": {Config: ServerConfig{URL: "https://apps.example/mcp", Enabled: true}},
			"local":      {Config: ServerConfig{Command: "mcp-local", Enabled: true}},
			"selected":   {Source: string(CatalogSourceSelectedPlugin), Config: ServerConfig{Command: "mcp-plugin", Enabled: true}},
			"remote":     {Config: ServerConfig{URL: "https://remote.example/mcp", EnvironmentID: "remote-1", Enabled: true}},
			"orphan":     {Config: ServerConfig{URL: "https://orphan.example/mcp", EnvironmentID: "remote-2", Enabled: true}},
			"disabled":   {Config: ServerConfig{Command: "mcp-disabled", Enabled: false}},
		},
	}
	runtimeConfig.SetServerPermissionProfiles(map[string]*sandbox.PermissionProfile{"remote-1": mcpTestEnvironmentProfile()})

	for _, name := range []string{"codex_apps", "local", "selected"} {
		profile, ok := runtimeConfig.PermissionProfileForServer(name)
		if !ok || profile == nil || profile.Disabled {
			t.Fatalf("%s profile = %#v, want thread (non-full-access) authority", name, profile)
		}
	}
	remote, ok := runtimeConfig.PermissionProfileForServer("remote")
	if !ok || remote == nil || !remote.Disabled {
		t.Fatalf("remote profile = %#v, want attached environment authority", remote)
	}
	for _, name := range []string{"orphan", "disabled"} {
		if _, ok := runtimeConfig.PermissionProfileForServer(name); ok {
			t.Fatalf("%s must have no published authority", name)
		}
	}
}

// Mirrors Rust PreparedMcpCall::new returning None on the production MCP tool
// execution path (#40728).
func TestMCPToolExecutorRejectsServerWithoutPublishedAuthorityLikeRust(t *testing.T) {
	allowed := mcpTestThreadProfile()
	service := NewMCPService(&RuntimeConfig{
		ServerPermissionProfiles: map[string]*sandbox.PermissionProfile{"allowed": allowed},
		Servers: map[string]ServerRegistration{
			"allowed": {Config: ServerConfig{Enabled: true}},
			"denied":  {Config: ServerConfig{Enabled: true}},
		},
	})
	invocation := func() *tool.Invocation {
		return &tool.Invocation{
			CallID:   "call-mcp",
			ToolName: tool.NamespacedName("denied", "run"),
			Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
		}
	}
	deniedExecutor := NewToolExecutor(&ToolExecutorOptions{
		Service:    service,
		ServerName: "denied",
		ToolInfo:   &MCPToolInfo{Name: "run"},
	})
	if _, err := deniedExecutor.Execute(context.Background(), invocation()); err == nil || !strings.Contains(err.Error(), "no published permission authority") {
		t.Fatalf("denied execute error = %v, want missing-authority rejection", err)
	}

	allowedExecutor := NewToolExecutor(&ToolExecutorOptions{
		Service:    service,
		ServerName: "allowed",
		ToolInfo:   &MCPToolInfo{Name: "run"},
	})
	allowedInvocation := invocation()
	allowedInvocation.ToolName = tool.NamespacedName("allowed", "run")
	if _, err := allowedExecutor.Execute(context.Background(), allowedInvocation); err != nil && strings.Contains(err.Error(), "no published permission authority") {
		t.Fatalf("allowed execute error = %v, want no authority rejection", err)
	}
}

func TestMCPThreadlessOperationsNeverInheritExecutionAuthority(t *testing.T) {
	runtimeConfig := &RuntimeConfig{
		PermissionProfile:    mcpTestEnvironmentProfile(),
		AvailableEnvironment: []string{"remote-1"},
		Servers: map[string]ServerRegistration{
			"codex_apps": {Config: ServerConfig{URL: "https://apps.example/mcp", Enabled: true}},
			"local":      {Config: ServerConfig{Command: "mcp-local", Enabled: true}},
			"remote":     {Config: ServerConfig{URL: "https://remote.example/mcp", EnvironmentID: "remote-1", Enabled: true}},
			"disabled":   {Config: ServerConfig{Command: "mcp-disabled", Enabled: false}},
		},
	}
	threadless := runtimeConfig.ForThreadlessOperations()
	if threadless == nil || threadless.PermissionProfile == nil || threadless.PermissionProfile.Disabled {
		t.Fatalf("threadless thread profile = %#v, want restrictive default", threadless)
	}
	for _, name := range []string{"codex_apps", "local", "remote"} {
		profile, ok := threadless.PermissionProfileForServer(name)
		if !ok || profile == nil || profile.Disabled {
			t.Fatalf("%s threadless profile = %#v, want restrictive default", name, profile)
		}
	}
	if _, ok := threadless.PermissionProfileForServer("disabled"); ok {
		t.Fatal("disabled server must have no threadless authority")
	}
}

// Mirrors Rust PreparedMcpCall::new returning None when the server's authority
// is unavailable: the prepared call is rejected instead of inheriting another
// owner's permissions.
func TestMCPBindingRejectsServerWithoutPublishedAuthorityLikeRust(t *testing.T) {
	allowed := mcpTestThreadProfile()
	service := NewMCPService(&RuntimeConfig{
		ServerPermissionProfiles: map[string]*sandbox.PermissionProfile{"allowed": allowed},
		Servers: map[string]ServerRegistration{
			"allowed": {Config: ServerConfig{Enabled: true}},
			"denied":  {Config: ServerConfig{Enabled: true}},
		},
	})
	if !service.HasPublishedPermissionAuthority() {
		t.Fatal("service should report published authority")
	}
	if _, ok := service.PermissionProfileForServer("allowed"); !ok {
		t.Fatal("allowed server should have published authority")
	}
	if _, ok := service.PermissionProfileForServer("denied"); ok {
		t.Fatal("denied server should have no published authority")
	}
	binding := service.CaptureBinding([]MCPServerStatus{
		{Name: "allowed", Tools: []MCPToolInfo{{Name: "run"}}},
		{Name: "denied", Tools: []MCPToolInfo{{Name: "run"}}},
	})
	if _, err := binding.CallTool(&MCPToolCallParams{Server: "denied", Tool: "run"}); err == nil || !strings.Contains(err.Error(), "no published permission authority") {
		t.Fatalf("denied call error = %v, want missing-authority rejection", err)
	}
	if _, err := binding.CallTool(&MCPToolCallParams{Server: "allowed", Tool: "run"}); err != nil && strings.Contains(err.Error(), "no published permission authority") {
		t.Fatalf("allowed call error = %v, want no authority rejection", err)
	}
}

// A runtime that never published authority keeps the legacy behavior.
func TestMCPBindingWithoutPublishedAuthoritySkipsEnforcement(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"plain": {Config: ServerConfig{Enabled: true}},
	}})
	if service.HasPublishedPermissionAuthority() {
		t.Fatal("service should not report published authority")
	}
	binding := service.CaptureBinding([]MCPServerStatus{{Name: "plain", Tools: []MCPToolInfo{{Name: "run"}}}})
	if _, err := binding.CallTool(&MCPToolCallParams{Server: "plain", Tool: "run"}); err != nil && strings.Contains(err.Error(), "no published permission authority") {
		t.Fatalf("plain call error = %v, want no authority rejection", err)
	}
}
