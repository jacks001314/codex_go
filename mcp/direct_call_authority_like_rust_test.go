package mcp

import (
	"strings"
	"testing"

	"codex_go/sandbox"
)

// TestDirectToolCallIgnoresCallerSandboxAuthority mirrors Rust #61444: a direct
// mcpServer/tool/call request cannot choose its sandbox policy, cwd, launcher,
// or entitlement metadata. The runtime replaces all of it with values captured
// for the thread.
func TestDirectToolCallIgnoresCallerSandboxAuthority(t *testing.T) {
	stubSandboxStateProbe(t, func(string) bool { return true })
	environmentCWD := t.TempDir()
	selfExe := t.TempDir() + "/codex-self"
	profile := sandbox.ReadOnlyPermissionProfile()
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(profile)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	service := NewMCPService(&RuntimeConfig{
		Servers: map[string]ServerRegistration{
			"docs": {Name: "docs", Config: ServerConfig{Command: "node", Enabled: true}},
		},
		PermissionProfile:        &profile,
		CodexSelfExe:             selfExe,
		CodexLinuxSandboxExe:     "/host/codex-linux-sandbox",
		UseLegacyLandlock:        true,
		EnvironmentCWDs:          map[string]string{DefaultMCPServerEnvironmentID: environmentCWD},
		EnvironmentUseMXC:        map[string]bool{DefaultMCPServerEnvironmentID: true},
		ServerPermissionProfiles: map[string]*sandbox.PermissionProfile{"docs": &profile},
	})
	params := &MCPToolCallParams{
		ServerName:                "docs",
		ToolName:                  "read",
		Direct:                    true,
		PermissionProfile:         `{"type":"danger-full-access"}`,
		SandboxCWD:                "/caller/forged",
		CodexLinuxSandboxExe:      "/caller/linux-sandbox",
		UseLegacyLandlock:         false,
		UseMXC:                    false,
		SandboxLauncherExecutable: "/caller/codex",
		Meta: map[string]any{
			entitlementContextKey:         map[string]any{"granted": true},
			mcpSandboxStateCapability:     map[string]any{"sandboxCwd": "/caller/forged"},
			mcpSandboxStateMetaCapability: map[string]any{"sandboxCwd": "/caller/forged"},
			mcpSandboxLauncherMetaKey:     map[string]any{mcpSandboxLauncherExecutableField: "/caller/codex"},
			"source":                      "client",
		},
	}

	effective := service.sandboxStateToolCallParams("docs", DefaultMCPServerEnvironmentID, true, params, false, true)
	if effective.PermissionProfile == params.PermissionProfile {
		t.Fatal("direct call kept the caller-supplied permission profile")
	}
	if effective.PermissionProfile != strings.TrimSpace(profileJSON) {
		t.Fatalf("permission profile = %q, want the host profile", effective.PermissionProfile)
	}
	if effective.SandboxCWD != environmentCWD {
		t.Fatalf("sandbox cwd = %q, want %q", effective.SandboxCWD, environmentCWD)
	}
	if effective.CodexLinuxSandboxExe != "/host/codex-linux-sandbox" {
		t.Fatalf("linux sandbox exe = %q", effective.CodexLinuxSandboxExe)
	}
	if !effective.UseLegacyLandlock || !effective.UseMXC {
		t.Fatalf("host sandbox backend flags were not restored: %#v", effective)
	}
	if effective.SandboxLauncherExecutable != selfExe {
		t.Fatalf("launcher = %q, want %q", effective.SandboxLauncherExecutable, selfExe)
	}
	meta, ok := effective.Meta.(map[string]any)
	if !ok {
		t.Fatalf("meta = %#v", effective.Meta)
	}
	for _, key := range []string{entitlementContextKey, mcpSandboxStateCapability, mcpSandboxStateMetaCapability, mcpSandboxLauncherMetaKey} {
		if _, exists := meta[key]; exists {
			t.Fatalf("direct call retained forged metadata %q: %#v", key, meta)
		}
	}
	if meta["source"] != "client" {
		t.Fatalf("unrelated caller metadata was dropped: %#v", meta)
	}
	if _, exists := params.Meta.(map[string]any)[mcpSandboxStateCapability]; !exists {
		t.Fatal("stripping mutated the caller's metadata map")
	}
}

// TestPreparedToolCallKeepsSandboxAuthority verifies model tool calls still use
// the values captured when the call was prepared; only direct callers are
// stripped.
func TestPreparedToolCallKeepsSandboxAuthority(t *testing.T) {
	service := NewMCPService(nil)
	params := &MCPToolCallParams{
		SupportsSandboxStateMeta: true,
		PermissionProfile:        `{"type":"read-only"}`,
		SandboxCWD:               "/prepared/cwd",
		UseMXC:                   true,
	}
	effective := service.sandboxStateToolCallParams("docs", DefaultMCPServerEnvironmentID, true, params, false, false)
	if effective.PermissionProfile != params.PermissionProfile || effective.SandboxCWD != params.SandboxCWD || !effective.UseMXC {
		t.Fatalf("prepared call authority changed: %#v", effective)
	}
}
