package mcp

import (
	"path/filepath"
	"testing"

	"codex_go/sandbox"
	"codex_go/tool"
)

// Mirrors Rust #59489: an environment-owned MCP call records the owner's
// prepared permissions with the live invocation, while a thread-owned server
// records none.
func TestToolInvocationCapturesPreparedOwnerPermissionsLikeRust(t *testing.T) {
	root := t.TempDir()
	ownerCWD := filepath.Join(root, "owner")
	threadCWD := filepath.Join(root, "thread")
	ownerProfile := sandbox.FullAccessPermissionProfile()
	threadProfile := sandbox.ReadOnlyPermissionProfile()
	servers := map[string]ServerRegistration{
		"local":  {Config: ServerConfig{Command: "local-server", Enabled: true}},
		"remote": {Config: ServerConfig{URL: "https://remote.example/mcp", EnvironmentID: "remote", Enabled: true}},
	}
	runtime := &RuntimeConfig{
		Servers:           servers,
		PermissionProfile: &threadProfile,
		HostCWD:           threadCWD,
		EnvironmentCWDs:   map[string]string{"remote": ownerCWD},
	}
	runtime.SetServerPermissionProfiles(map[string]*sandbox.PermissionProfile{"remote": &ownerProfile})
	service := NewMCPService(runtime)
	binding := service.CaptureBinding([]MCPServerStatus{
		{Name: "local", State: MCPServerReady, Tools: []MCPToolInfo{{Name: "read"}}},
		{Name: "remote", State: MCPServerReady, Tools: []MCPToolInfo{{Name: "query"}}},
	})

	owner := NewToolExecutor(&ToolExecutorOptions{
		Service:    service,
		ServerName: "remote",
		ToolInfo:   &MCPToolInfo{Name: "query"},
		ToolName:   tool.NamespacedName("remote", "query"),
		Binding:    binding,
		ThreadID:   "thread-1",
		TurnID:     "turn-1",
	})
	metadata := owner.invocationMetadata(map[string]any{"q": "x"})
	if metadata.PermissionSource == nil {
		t.Fatal("environment-owned call recorded no owner permissions")
	}
	source := metadata.PermissionSource
	if source.Thread || source.EnvironmentID != "remote" || source.CWD != ownerCWD ||
		source.Profile == nil || !source.Profile.Disabled {
		t.Fatalf("captured owner permissions = %#v, want the prepared remote owner at %s", source, ownerCWD)
	}
	if metadata.ThreadID != "thread-1" || metadata.TurnID != "turn-1" || metadata.ServerName != "remote" {
		t.Fatalf("invocation metadata = %#v, want the call's own identity", metadata)
	}

	threadOwned := NewToolExecutor(&ToolExecutorOptions{
		Service:    service,
		ServerName: "local",
		ToolInfo:   &MCPToolInfo{Name: "read"},
		ToolName:   tool.NamespacedName("local", "read"),
		Binding:    binding,
	})
	if got := threadOwned.invocationMetadata(nil); got.PermissionSource != nil {
		t.Fatalf("thread-owned call recorded owner permissions: %#v", got.PermissionSource)
	}
}
