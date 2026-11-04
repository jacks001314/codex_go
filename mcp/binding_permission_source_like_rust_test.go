package mcp

import (
	"path/filepath"
	"testing"

	"codex_go/sandbox"
)

// Mirrors Rust #59020: a prepared call retains the policy and directory
// context that produced it even after the live MCP runtime publishes a refresh.
func TestPreparedBindingKeepsPermissionSourceAcrossRefreshLikeRust(t *testing.T) {
	root := t.TempDir()
	oldHostCWD := filepath.Join(root, "old", "host")
	oldEnvironmentCWD := filepath.Join(root, "old", "executor")
	newHostCWD := filepath.Join(root, "new", "host")
	newEnvironmentCWD := filepath.Join(root, "new", "executor")
	threadProfile := sandbox.ReadOnlyPermissionProfile()
	oldEnvironmentProfile := sandbox.FullAccessPermissionProfile()
	newEnvironmentProfile := sandbox.ReadOnlyPermissionProfile()
	servers := map[string]ServerRegistration{
		"local":  {Config: ServerConfig{Command: "local-server", Enabled: true}},
		"remote": {Config: ServerConfig{URL: "https://remote.example/mcp", EnvironmentID: "remote", Enabled: true}},
	}
	runtime := &RuntimeConfig{
		Servers:           servers,
		PermissionProfile: &threadProfile,
		HostCWD:           oldHostCWD,
		EnvironmentCWDs:   map[string]string{"remote": oldEnvironmentCWD},
	}
	runtime.SetServerPermissionProfiles(map[string]*sandbox.PermissionProfile{"remote": &oldEnvironmentProfile})
	service := NewMCPService(runtime)
	binding := service.CaptureBinding([]MCPServerStatus{
		{Name: "local", State: MCPServerReady, Tools: []MCPToolInfo{{Name: "read"}}},
		{Name: "remote", State: MCPServerReady, Tools: []MCPToolInfo{{Name: "query"}}},
	})

	local, ok := binding.permissionSources["local"]
	if !ok || !local.Thread || local.CWD != oldHostCWD || local.Profile == nil || !effectiveMPCProfileIsReadOnly(local.Profile) {
		t.Fatalf("local prepared source = %#v, want the old thread policy at %s", local, oldHostCWD)
	}
	remote, ok := binding.permissionSources["remote"]
	if !ok || remote.Thread || remote.EnvironmentID != "remote" || remote.CWD != oldEnvironmentCWD || remote.Profile == nil || !remote.Profile.Disabled {
		t.Fatalf("remote prepared source = %#v, want the old environment policy at %s", remote, oldEnvironmentCWD)
	}

	refreshed := &RuntimeConfig{
		Servers:           servers,
		PermissionProfile: &threadProfile,
		HostCWD:           newHostCWD,
		EnvironmentCWDs:   map[string]string{"remote": newEnvironmentCWD},
	}
	refreshed.SetServerPermissionProfiles(map[string]*sandbox.PermissionProfile{"remote": &newEnvironmentProfile})
	service.ApplyRuntimeConfig(refreshed)

	if got := service.serverPermissionSources["local"].CWD; got != newHostCWD {
		t.Fatalf("refreshed local cwd = %q, want %q", got, newHostCWD)
	}
	if got := service.serverPermissionSources["remote"].CWD; got != newEnvironmentCWD {
		t.Fatalf("refreshed remote cwd = %q, want %q", got, newEnvironmentCWD)
	}
	if got := binding.permissionSources["local"].CWD; got != oldHostCWD {
		t.Fatalf("prepared local cwd changed to %q", got)
	}
	if got := binding.permissionSources["remote"].CWD; got != oldEnvironmentCWD {
		t.Fatalf("prepared remote cwd changed to %q", got)
	}

	// Approval code reads the captured source through the binding API; it must
	// still describe the environment that prepared the call after the refresh.
	prepared, ok := binding.PermissionSourceForServer("remote")
	if !ok || prepared.Thread || prepared.EnvironmentID != "remote" ||
		prepared.CWD != oldEnvironmentCWD || prepared.Profile == nil || !prepared.Profile.Disabled {
		t.Fatalf("binding permission source = %#v, want the prepared remote owner", prepared)
	}
	prepared.CWD = newEnvironmentCWD
	prepared.Profile.Disabled = false
	again, ok := binding.PermissionSourceForServer("remote")
	if !ok || again.CWD != oldEnvironmentCWD || again.Profile == nil || !again.Profile.Disabled {
		t.Fatalf("mutating the returned source changed the binding: %#v", again)
	}
	if _, ok := binding.PermissionSourceForServer("missing"); ok {
		t.Fatal("missing server resolved a permission source")
	}
	var nilBinding *Binding
	if _, ok := nilBinding.PermissionSourceForServer("remote"); ok {
		t.Fatal("nil binding resolved a permission source")
	}
}

func effectiveMPCProfileIsReadOnly(profile *sandbox.PermissionProfile) bool {
	return profile != nil && !profile.Disabled && profile.SandboxPolicy != nil &&
		profile.SandboxPolicy.Kind == sandbox.SandboxReadOnly
}
