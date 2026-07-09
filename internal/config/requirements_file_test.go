package config

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/internal/sandbox"
)

func TestLoadRequirementsFileParsesRustStyleTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requirements.toml")
	body := `
allowed_approval_policies = ["on-request"]
allowed_approvals_reviewers = ["user"]
allowed_sandbox_modes = ["read-only", "workspace-write"]
allowed_web_search_modes = []
allow_managed_hooks_only = true
allow_appshots = false
allow_remote_control = true
enforce_residency = "us"

[feature_requirements]
shell_tool = false
web_search = true

[hooks]
managed_dir = "/managed/hooks"
windows_managed_dir = "C:\\managed\\hooks"

[[hooks.PreToolUse]]
matcher = "shell"
hooks = [{ type = "command", command = "echo ok", timeout_sec = 5, async = true, status_message = "checking" }]

[network]
enabled = true
http_port = 1234
socks_port = 5678
allow_upstream_proxy = true
dangerously_allow_non_loopback_proxy = false
dangerously_allow_all_unix_sockets = true
managed_allowed_domains_only = false
allow_local_binding = true

[network.domains]
"a.example" = "allow"
"b.example" = "deny"

[network.unix_sockets]
"/tmp/a.sock" = "allow"
"/tmp/b.sock" = "deny"

[models.new_thread]
model = "gpt-5"
model_reasoning_effort = "high"
service_tier = "auto"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile requirements error = %v", err)
	}

	requirements, err := LoadRequirementsFile(path)
	if err != nil {
		t.Fatalf("LoadRequirementsFile returned error: %v", err)
	}
	if requirements == nil {
		t.Fatal("LoadRequirementsFile returned nil requirements")
	}
	if len(requirements.AllowedApprovalPolicies) != 1 || requirements.AllowedApprovalPolicies[0] != sandbox.ApprovalOnRequest {
		t.Fatalf("AllowedApprovalPolicies = %#v", requirements.AllowedApprovalPolicies)
	}
	if len(requirements.AllowedSandboxModes) != 2 || requirements.AllowedSandboxModes[1] != sandbox.SandboxWorkspaceWrite {
		t.Fatalf("AllowedSandboxModes = %#v", requirements.AllowedSandboxModes)
	}
	if requirements.AllowedWebSearchModes == nil || len(requirements.AllowedWebSearchModes) != 0 {
		t.Fatalf("AllowedWebSearchModes = %#v, want explicit empty slice", requirements.AllowedWebSearchModes)
	}
	if requirements.AllowManagedHooksOnly == nil || !*requirements.AllowManagedHooksOnly {
		t.Fatalf("AllowManagedHooksOnly = %#v", requirements.AllowManagedHooksOnly)
	}
	if requirements.AllowAppshots == nil || *requirements.AllowAppshots {
		t.Fatalf("AllowAppshots = %#v", requirements.AllowAppshots)
	}
	if requirements.FeatureRequirements["shell_tool"] || !requirements.FeatureRequirements["web_search"] {
		t.Fatalf("FeatureRequirements = %#v", requirements.FeatureRequirements)
	}
	if requirements.Hooks == nil || requirements.Hooks.ManagedDir == nil || *requirements.Hooks.ManagedDir != "/managed/hooks" {
		t.Fatalf("Hooks = %#v", requirements.Hooks)
	}
	if len(requirements.Hooks.PreToolUse) != 1 || len(requirements.Hooks.PreToolUse[0].Hooks) != 1 || requirements.Hooks.PreToolUse[0].Hooks[0].TimeoutSec == nil || *requirements.Hooks.PreToolUse[0].Hooks[0].TimeoutSec != 5 {
		t.Fatalf("Hooks.PreToolUse = %#v", requirements.Hooks.PreToolUse)
	}
	if requirements.Network == nil || requirements.Network.HTTPPort == nil || *requirements.Network.HTTPPort != 1234 {
		t.Fatalf("Network = %#v", requirements.Network)
	}
	if requirements.Network.Domains["a.example"] != NetworkAllow || requirements.Network.UnixSockets["/tmp/b.sock"] != NetworkDeny {
		t.Fatalf("Network permissions = domains %#v sockets %#v", requirements.Network.Domains, requirements.Network.UnixSockets)
	}
	if requirements.Models == nil || requirements.Models.NewThread == nil || requirements.Models.NewThread.Model == nil || *requirements.Models.NewThread.Model != "gpt-5" {
		t.Fatalf("Models = %#v", requirements.Models)
	}
}

func TestLoadRequirementsFileMissingReturnsNil(t *testing.T) {
	requirements, err := LoadRequirementsFile(filepath.Join(t.TempDir(), "requirements.toml"))
	if err != nil {
		t.Fatalf("LoadRequirementsFile missing returned error: %v", err)
	}
	if requirements != nil {
		t.Fatalf("requirements = %#v, want nil", requirements)
	}
}
