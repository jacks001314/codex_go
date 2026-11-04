package mcp

import (
	"context"
	"path/filepath"
	"testing"

	"codex_go/sandbox"
	"codex_go/tool"
)

// Mirroring Rust #59028, the approval request carries the permission source the
// model step captured, even if the server owner's live permissions changed
// before approval ran.
func TestMCPApprovalUsesPreparedOwnerPermissionsLikeRust(t *testing.T) {
	root := t.TempDir()
	oldCWD := filepath.Join(root, "old")
	newCWD := filepath.Join(root, "new")
	oldProfile := sandbox.FullAccessPermissionProfile()
	newProfile := sandbox.ReadOnlyPermissionProfile()
	servers := map[string]ServerRegistration{
		"remote": {Config: ServerConfig{URL: "https://remote.example/mcp", EnvironmentID: "remote", Enabled: true}},
	}
	runtime := &RuntimeConfig{Servers: servers, EnvironmentCWDs: map[string]string{"remote": oldCWD}}
	runtime.SetServerPermissionProfiles(map[string]*sandbox.PermissionProfile{"remote": &oldProfile})
	service := NewMCPService(runtime)
	binding := service.CaptureBinding([]MCPServerStatus{{
		Name: "remote", State: MCPServerReady, Tools: []MCPToolInfo{{Name: "query"}},
	}})

	var captured *MCPToolApprovalRequest
	executor := NewToolExecutor(&ToolExecutorOptions{
		Service:    service,
		ServerName: "remote",
		ToolInfo:   &MCPToolInfo{Name: "query"},
		ToolName:   tool.NamespacedName("remote", "query"),
		Binding:    binding,
		ToolApproval: &ToolApprovalOptions{
			ApprovalPolicy: sandbox.ApprovalOnRequest,
			Handler: MCPToolApprovalHandlerFunc(func(_ context.Context, request *MCPToolApprovalRequest) (MCPToolApprovalOutcome, error) {
				captured = request
				return MCPToolApprovalOutcome{Decision: MCPToolApprovalApprove}, nil
			}),
		},
	})

	refreshed := &RuntimeConfig{Servers: servers, EnvironmentCWDs: map[string]string{"remote": newCWD}}
	refreshed.SetServerPermissionProfiles(map[string]*sandbox.PermissionProfile{"remote": &newProfile})
	service.ApplyRuntimeConfig(refreshed)

	denied, err := executor.approveToolCallIfNeeded(context.Background(), "call-1", map[string]any{"q": "x"}, "")
	if err != nil {
		t.Fatalf("approveToolCallIfNeeded() error = %v", err)
	}
	if denied != nil {
		t.Fatalf("approveToolCallIfNeeded() denied = %#v", denied)
	}
	if captured == nil || captured.PreparedPermissionSource == nil {
		t.Fatalf("approval request = %#v, want a prepared permission source", captured)
	}
	source := captured.PreparedPermissionSource
	if source.Thread || source.EnvironmentID != "remote" || source.CWD != oldCWD ||
		source.Profile == nil || !source.Profile.Disabled {
		t.Fatalf("prepared source = %#v, want the old full-access owner at %s", source, oldCWD)
	}
	if captured.EnvironmentID != "remote" {
		t.Fatalf("approval environment = %q, want remote", captured.EnvironmentID)
	}
}
