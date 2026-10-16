package mcp

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex_go/sandbox"
)

// TestSandboxStateRoundTripFromMCPMetaToSandboxCLI is the MCP -> CLI wire check
// for #53709/#52182: the object an MCP host writes into tool-call `_meta` must
// be exactly what `codex sandbox --sandbox-state-json` parses. Rust's
// authoritative spelling is camelCase, so the payload here is camelCase.
func TestSandboxStateRoundTripFromMCPMetaToSandboxCLI(t *testing.T) {
	service := NewMCPService(nil)
	profile := sandbox.WorkspaceWritePermissionProfile()
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(profile)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	cwd := t.TempDir()

	meta, ok := service.augmentToolCallMeta(&MCPToolCallParams{
		SupportsSandboxStateMeta: true,
		PermissionProfile:        profileJSON,
		SandboxCWD:               cwd,
		UseMXC:                   true,
	}).(map[string]any)
	if !ok {
		t.Fatal("meta should be an object")
	}
	encoded, err := json.Marshal(meta[mcpSandboxStateCapability])
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, key := range []string{`"permissionProfile":`, `"sandboxCwd":`, `"useMxc":true`} {
		if !strings.Contains(text, key) {
			t.Fatalf("camelCase key %s missing from the emitted state: %s", key, text)
		}
	}

	plan, err := sandbox.BuildCommandRunPlan(&sandbox.CommandRunRequest{
		SandboxStateJSON: text,
		Command:          []string{"go", "version"},
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan() rejected the MCP state: %v", err)
	}
	if plan.PermissionProfile == nil || plan.PermissionProfile.Disabled {
		t.Fatalf("forwarded policy was not applied: %#v", plan.PermissionProfile)
	}
	if filepath.Clean(plan.CWD) != filepath.Clean(cwd) {
		t.Fatalf("plan cwd = %q, want the forwarded sandbox cwd %q", plan.CWD, cwd)
	}
	if runtime.GOOS == "windows" {
		// Rust debug_sandbox (#52182): a forwarded useMxc selects the native MXC
		// backend instead of the restricted token.
		if plan.SandboxType != sandbox.SandboxTypeWindowsMxc {
			t.Fatalf("plan sandbox type = %q, want %q", plan.SandboxType, sandbox.SandboxTypeWindowsMxc)
		}
	}
}

// TestToolCallSandboxStateWiringIsProductionReachable drives the exact sequence
// a tools/call performs (prepareToolCallSandboxState) against a connection that
// negotiated the capability, and asserts the emitted `_meta` carries both state
// keys plus the verified launcher. The capability comes from the connection,
// never from the caller.
func TestToolCallSandboxStateWiringIsProductionReachable(t *testing.T) {
	stubSandboxStateProbe(t, func(exe string) bool { return filepath.Base(exe) == "codex-self.exe" })
	cwd := t.TempDir()
	environmentCWD := t.TempDir()
	selfExe := filepath.Join(t.TempDir(), "codex-self.exe")

	profile := sandbox.ReadOnlyPermissionProfile()
	service := NewMCPService(&RuntimeConfig{
		Servers: map[string]ServerRegistration{
			"docs": {
				Name:   "docs",
				Config: ServerConfig{Command: "node", Enabled: true, CWD: cwd},
			},
		},
		PermissionProfile:        &profile,
		ServerPermissionProfiles: map[string]*sandbox.PermissionProfile{"docs": &profile},
		CodexSelfExe:             selfExe,
		CodexLinuxSandboxExe:     "/usr/bin/codex-linux-sandbox",
		UseLegacyLandlock:        true,
		EnvironmentCWDs:          map[string]string{DefaultMCPServerEnvironmentID: environmentCWD},
		EnvironmentUseMXC:        map[string]bool{DefaultMCPServerEnvironmentID: true},
	})

	config, ok := service.serverConfig("docs")
	if !ok {
		t.Fatal("the registered server should be enabled")
	}
	// Stand in for the connection the initialize/server-discover handshake
	// produces: the negotiated capability is recorded on it, not on the caller.
	service.stdioClients["docs"] = &cachedMCPStdioClient{
		key:    mcpConnectionCacheKey(&config, false),
		client: &stdioClient{initialized: true, supportsSandboxStateMetaCapability: true},
	}
	client := service.stdioClientForServer("docs", &config)
	if client == nil || !client.sandboxStateMetaNegotiated() {
		t.Fatal("the negotiated stdio connection should report the sandbox-state capability")
	}

	effective, meta := service.prepareToolCallSandboxState(
		"docs",
		config.EffectiveEnvironmentID(),
		true,
		&MCPToolCallParams{ServerName: "docs", ToolName: "read", Arguments: map[string]any{}, ThreadID: "thread-1"},
		false,
		client.sandboxStateMetaNegotiated(),
	)

	if !effective.SupportsSandboxStateMeta {
		t.Fatal("a negotiated capability must enable sandbox-state metadata")
	}
	if effective.PermissionProfile == "" {
		t.Fatal("the published server authority must fill the forwarded policy")
	}
	if effective.SandboxCWD != filepath.Clean(environmentCWD) && effective.SandboxCWD != environmentCWD {
		t.Fatalf("sandbox cwd = %q, want the environment cwd %q", effective.SandboxCWD, environmentCWD)
	}
	if effective.CodexLinuxSandboxExe != "/usr/bin/codex-linux-sandbox" {
		t.Fatalf("codex linux sandbox exe = %q", effective.CodexLinuxSandboxExe)
	}
	if !effective.UseLegacyLandlock {
		t.Fatal("the runtime's legacy-landlock setting must be forwarded")
	}
	if !effective.UseMXC {
		t.Fatal("the environment's captured MXC backend must be forwarded")
	}
	if effective.SandboxLauncherExecutable != selfExe {
		t.Fatalf("launcher executable = %q, want the verified host binary %q", effective.SandboxLauncherExecutable, selfExe)
	}
	if effective.ServerEnvironmentID != DefaultMCPServerEnvironmentID {
		t.Fatalf("server environment = %q", effective.ServerEnvironmentID)
	}

	metaMap, ok := meta.(map[string]any)
	if !ok {
		t.Fatal("_meta should be an object")
	}
	if _, exists := metaMap[mcpSandboxStateCapability]; !exists {
		t.Fatalf("the experimental state key is missing: %#v", metaMap)
	}
	if _, exists := metaMap[mcpSandboxStateMetaCapability]; !exists {
		t.Fatalf("the deprecated state key is missing: %#v", metaMap)
	}
	launcher, ok := metaMap[mcpSandboxLauncherMetaKey].(map[string]any)
	if !ok || launcher[mcpSandboxLauncherExecutableField] != selfExe {
		t.Fatalf("launcher metadata = %#v", metaMap[mcpSandboxLauncherMetaKey])
	}
}
