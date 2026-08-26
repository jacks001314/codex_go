package tui

import (
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
)

func TestDebugConfigOutputListsLayersIncludingDisabled(t *testing.T) {
	disabled := "not trusted"
	read := &config.ConfigReadResponse{Layers: []config.Layer{
		{
			Name:    config.LayerSource{Type: config.LayerSourceSystem, File: `/etc/codex/config.toml`},
			Version: "system-v1",
			Config:  map[string]any{"model": "gpt-system"},
		},
		{
			Name:           config.LayerSource{Type: config.LayerSourceProject, DotCodexFolder: `D:\repo\.gcode`},
			Version:        "project-v1",
			Config:         map[string]any{"model": "gpt-project"},
			DisabledReason: &disabled,
		},
	}}

	rendered := strings.Join(RenderDebugConfigLines(read, nil, nil), "\n")
	for _, want := range []string{
		DebugConfigCommand,
		"Config layer stack (lowest precedence first):",
		"1. system (/etc/codex/config.toml) (enabled)",
		`2. project (D:\repo\.gcode/config.toml) (disabled)`,
		"reason: not trusted",
		"Requirements:",
		"<none>",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("debug config output missing %q:\n%s", want, rendered)
		}
	}
}

func TestDebugConfigOutputListsSessionFlagKeyValuePairs(t *testing.T) {
	read := &config.ConfigReadResponse{Layers: []config.Layer{{
		Name: config.LayerSource{Type: config.LayerSourceSessionFlags},
		Config: map[string]any{
			"model": "gpt-5",
			"features": map[string]any{
				"shell_tool": true,
				"web_search": false,
			},
		},
	}}}

	rendered := strings.Join(RenderDebugConfigLines(read, nil, nil), "\n")
	modelIndex := strings.Index(rendered, `     - model = "gpt-5"`)
	shellIndex := strings.Index(rendered, "     - features.shell_tool = true")
	webIndex := strings.Index(rendered, "     - features.web_search = false")
	if shellIndex < 0 || webIndex < 0 || modelIndex < 0 {
		t.Fatalf("session flag details missing:\n%s", rendered)
	}
	if !(shellIndex < webIndex && webIndex < modelIndex) {
		t.Fatalf("session flag details not sorted like Rust:\n%s", rendered)
	}
}

func TestDebugConfigOutputShowsManagedLayerValues(t *testing.T) {
	read := &config.ConfigReadResponse{Layers: []config.Layer{
		{
			Name:   config.LayerSource{Type: config.LayerSourceLegacyManagedConfigFromMDM},
			Config: "model = \"managed\"",
		},
		{
			Name: config.LayerSource{Type: config.LayerSourceEnterpriseManaged, ID: "team", Name: "Team Policy"},
			Config: map[string]any{
				"model": "gpt-enterprise",
			},
		},
	}}

	rendered := strings.Join(RenderDebugConfigLines(read, nil, nil), "\n")
	for _, want := range []string{
		"legacy managed_config.toml (MDM) (enabled)",
		`MDM value: "model = \"managed\""`,
		"enterprise-managed (Team Policy, team) (enabled)",
		`Enterprise-managed config value: {"model":"gpt-enterprise"}`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("managed layer output missing %q:\n%s", want, rendered)
		}
	}
}

func TestDebugConfigOutputListsRequirementsLikeRust(t *testing.T) {
	allow := true
	deny := false
	httpPort := uint16(1234)
	socksPort := uint16(5678)
	managedDir := `/managed/hooks`
	windowsManagedDir := `C:\managed\hooks`
	residency := config.ResidencyUS
	requirements := &config.ConfigRequirements{
		AllowedApprovalPolicies:   []sandbox.AskForApproval{sandbox.ApprovalOnRequest},
		AllowedApprovalsReviewers: []config.ApprovalsReviewer{config.ApprovalsReviewerUser},
		AllowedSandboxModes: []sandbox.SandboxMode{
			sandbox.SandboxReadOnly,
			sandbox.SandboxWorkspaceWrite,
			sandbox.SandboxDangerFullAccess,
		},
		AllowedWebSearchModes: []config.WebSearchMode{config.WebSearchEnabled},
		AllowManagedHooksOnly: &allow,
		AllowAppshots:         &deny,
		AllowRemoteControl:    &allow,
		FeatureRequirements:   map[string]bool{"web_search": true, "shell_tool": false},
		Hooks: &config.ManagedHooksRequirements{
			ManagedDir:        &managedDir,
			WindowsManagedDir: &windowsManagedDir,
			PreToolUse: []config.ConfiguredHookGroup{{
				Hooks: []config.ConfiguredHookHandler{{Type: "command", Command: "echo ok"}},
			}},
			PostToolUse: []config.ConfiguredHookGroup{{
				Hooks: []config.ConfiguredHookHandler{{Type: "prompt"}},
			}},
		},
		EnforceResidency: &residency,
		Network: &config.NetworkRequirements{
			Enabled:                          &allow,
			HTTPPort:                         &httpPort,
			SOCKSPort:                        &socksPort,
			AllowUpstreamProxy:               &allow,
			DangerouslyAllowNonLoopbackProxy: &deny,
			DangerouslyAllowAllUnixSockets:   &allow,
			Domains: map[string]config.NetworkPermission{
				"b.example": config.NetworkDeny,
				"a.example": config.NetworkAllow,
			},
			ManagedAllowedDomainsOnly: &deny,
			UnixSockets: map[string]config.NetworkPermission{
				"/tmp/a.sock": config.NetworkAllow,
				"/tmp/b.sock": config.NetworkDeny,
			},
			AllowLocalBinding: &allow,
		},
	}

	rendered := strings.Join(RenderDebugConfigLines(nil, requirements, func(mode sandbox.SandboxMode) bool {
		return mode != sandbox.SandboxDangerFullAccess
	}), "\n")
	for _, want := range []string{
		"allowed_approval_policies: on-request (source: config requirements)",
		"allowed_approvals_reviewers: user (source: config requirements)",
		"allowed_sandbox_modes: read-only, workspace-write (source: config requirements)",
		"allowed_web_search_modes: enabled, disabled (source: config requirements)",
		"allow_managed_hooks_only: true (source: config requirements)",
		"allow_appshots: false (source: config requirements)",
		"allow_remote_control: true (source: config requirements)",
		"features: shell_tool=false, web_search=true (source: config requirements)",
		`hooks: managed_dir=/managed/hooks, windows_managed_dir=C:\managed\hooks, handlers=2 (source: config requirements)`,
		"enforce_residency: us (source: config requirements)",
		"experimental_network: enabled=true, http_port=1234, socks_port=5678, allow_upstream_proxy=true, dangerously_allow_non_loopback_proxy=false, dangerously_allow_all_unix_sockets=true, domains={a.example=allow, b.example=deny}, managed_allowed_domains_only=false, unix_sockets={/tmp/a.sock=allow, /tmp/b.sock=deny}, allow_local_binding=true (source: config requirements)",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("requirements output missing %q:\n%s", want, rendered)
		}
	}
}

func TestDebugConfigOutputNormalizesEmptyWebSearchModes(t *testing.T) {
	requirements := &config.ConfigRequirements{AllowedWebSearchModes: []config.WebSearchMode{}}

	rendered := strings.Join(RenderDebugConfigLines(nil, requirements, nil), "\n")
	if !strings.Contains(rendered, "allowed_web_search_modes: disabled (source: config requirements)") {
		t.Fatalf("empty web search modes were not normalized:\n%s", rendered)
	}
}

func TestDebugConfigSessionAllProxyURLMatchesRust(t *testing.T) {
	if got := SessionAllProxyURL("127.0.0.1:1234", "127.0.0.1:5678", true); got != "socks5h://127.0.0.1:5678" {
		t.Fatalf("socks all_proxy = %q", got)
	}
	if got := SessionAllProxyURL("127.0.0.1:1234", "127.0.0.1:5678", false); got != "http://127.0.0.1:1234" {
		t.Fatalf("http all_proxy = %q", got)
	}
}
