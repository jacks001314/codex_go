package app

import (
	"strings"

	"codex_go/config"
	"codex_go/sandbox"
)

// Upstream #49160 ("Support projectless TUI sessions with workspace defaults")
// has two cooperating decisions, mirrored here:
//
//   - codex-rs/tui/src/config_update.rs: a positively discovered projectless
//     folder without a saved trust decision needs no folder-trust decision at
//     all, so `read_remote_project_trust` returns `Ok(None)` and the trust
//     widget is never rendered.
//   - codex-rs/tui/src/projectless.rs: such a folder takes implicit
//     workspace-write permissions plus granular approval, but only while local
//     execution, configuration, and managed policy allow it.

// projectlessImplicitPermissionKeys are the effective-config keys that keep a
// folder on its configured permissions instead of the implicit projectless
// defaults. They are the union of the two guard lists in
// codex-rs/tui/src/projectless.rs: the server defaults may not carry a sandbox
// mode / workspace-write sandbox / default_permissions / permissions / network
// value, and neither may the effective config.
var projectlessImplicitPermissionKeys = []string{
	"sandbox_mode",
	"sandbox_workspace_write",
	"default_permissions",
	"permissions",
	"network",
}

// ProjectlessFolderTrustInputs carries the guard inputs for both decisions.
type ProjectlessFolderTrustInputs struct {
	// CWD is the folder the session would run in.
	CWD string
	// Local reports that execution stays on this host - Rust
	// `host == ProjectTrustHost::Local` / `!app_server.uses_remote_workspace()`
	// (and, for codex-rs/tui/src/projectless.rs,
	// `has_only_local_environments`).
	Local bool
	// Markers is the effective `project_root_markers` list; empty means the
	// default markers (Rust `unwrap_or_else(default_project_root_markers)`).
	Markers []string
	// ProjectLayers counts the `project` layers the server reported
	// (config/read with includeLayers). Rust requires
	// `project_layers.is_empty()` before local discovery may call a folder
	// projectless, and `config_layer_stack.is_projectless()` afterwards.
	ProjectLayers int
	// Effective is the effective configuration that would otherwise supply the
	// implicit permission keys above.
	Effective map[string]any
	// Requirements is the managed requirements set.
	Requirements *config.ConfigRequirements
	// SavedTrustDecision reports an existing `projects.<path>.trust_level`.
	SavedTrustDecision bool
	// ExplicitOverrides reports launch-time sandbox / permission overrides
	// (Rust ConfigOverrides.sandbox_mode / permission_profile /
	// default_permissions).
	ExplicitOverrides bool
}

// ProjectlessFolderTrustEligible reports whether a folder may skip the folder
// trust prompt. It mirrors the projectless half of
// `read_remote_project_trust` (codex-rs/tui/src/config_update.rs):
//
//	projectless = discover_project_root(cwd, configured_markers).is_none()
//	    && discover_project_root(cwd, default_project_root_markers()).is_none();
//	if !explicitly_untrusted
//	    && (trust_level == Some(Trusted) || trust_level.is_none() && projectless || ...)
//	{ return Ok(None); }
//
// A saved decision and explicit launch overrides both keep the configured
// behavior, so the prompt is still skipped only for a folder that contributes
// no project configuration.
func ProjectlessFolderTrustEligible(in ProjectlessFolderTrustInputs) bool {
	if !in.Local || strings.TrimSpace(in.CWD) == "" {
		return false
	}
	if in.ProjectLayers > 0 {
		return false
	}
	// Rust: `trust_level.is_none()` - a saved decision (trusted or untrusted)
	// keeps the configured behavior.
	if in.SavedTrustDecision {
		return false
	}
	return config.DiscoveredProjectlessFolder(in.CWD, in.Markers)
}

// ProjectlessFolderDefaults is the implicit permission selection #49160 applies
// to an eligible projectless folder: the built-in workspace profile plus
// granular approval where only `request_permissions` and `mcp_elicitations`
// remain enabled.
type ProjectlessFolderDefaults struct {
	PermissionProfileID string
	SandboxMode         sandbox.SandboxMode
	ApprovalPolicy      sandbox.AskForApproval
	Granular            sandbox.GranularApprovalConfig
}

// ProjectlessImplicitDefaults ports `projectless::apply_defaults`
// (codex-rs/tui/src/projectless.rs). It reports ok=false when any guard keeps
// the configured permission selection, including:
//
//   - a project config layer, remote execution, workspace roots beyond the cwd,
//     a saved trust decision, or launch overrides;
//   - a configured sandbox_mode / sandbox_workspace_write /
//     default_permissions / permissions / network value;
//   - managed `default_permissions` (requirements_toml().default_permissions);
//   - a managed allow-list that does not admit the workspace profile
//     (`is_permission_profile_allowed(BUILT_IN_PERMISSION_PROFILE_WORKSPACE,
//     PermissionProfile::workspace_write())`).
func ProjectlessImplicitDefaults(in ProjectlessFolderTrustInputs) (ProjectlessFolderDefaults, bool) {
	if !ProjectlessFolderTrustEligible(in) {
		return ProjectlessFolderDefaults{}, false
	}
	// Rust: `overrides.sandbox_mode.is_some() || overrides.permission_profile.is_some()
	// || overrides.default_permissions.is_some()`.
	if in.ExplicitOverrides {
		return ProjectlessFolderDefaults{}, false
	}
	for _, key := range projectlessImplicitPermissionKeys {
		if effectiveConfigValueSet(in.Effective, key) {
			return ProjectlessFolderDefaults{}, false
		}
	}
	if in.Requirements != nil {
		if in.Requirements.DefaultPermissions != nil {
			return ProjectlessFolderDefaults{}, false
		}
		if !config.PermissionProfileAllowedByRequirements(
			in.Requirements.AllowedPermissionProfiles,
			sandbox.BuiltInPermissionProfileWorkspace,
		) {
			return ProjectlessFolderDefaults{}, false
		}
		if !projectlessSandboxModeAllowed(in.Requirements) {
			return ProjectlessFolderDefaults{}, false
		}
	}
	return projectlessWorkspaceWriteDefaults(), true
}

// projectlessWorkspaceWriteDefaults is the default selection itself; kept
// separate so the live thread-start path can reuse it without re-deriving the
// guards.
func projectlessWorkspaceWriteDefaults() ProjectlessFolderDefaults {
	return ProjectlessFolderDefaults{
		PermissionProfileID: sandbox.BuiltInPermissionProfileWorkspace,
		SandboxMode:         sandbox.SandboxWorkspaceWrite,
		ApprovalPolicy:      sandbox.ApprovalGranular,
		Granular: sandbox.GranularApprovalConfig{
			SandboxApproval:    false,
			Rules:              false,
			SkillApproval:      false,
			RequestPermissions: true,
			MCPElicitations:    true,
		},
	}
}

// projectlessApprovalPolicyParam renders the granular approval choice the way
// thread/start carries it (Rust's externally tagged `AskForApproval::Granular`
// object, see appserver granularApprovalConfigForTurn).
func projectlessApprovalPolicyParam(defaults ProjectlessFolderDefaults) map[string]any {
	return map[string]any{
		"granular": map[string]any{
			"sandbox_approval":    defaults.Granular.SandboxApproval,
			"rules":               defaults.Granular.Rules,
			"skill_approval":      defaults.Granular.SkillApproval,
			"request_permissions": defaults.Granular.RequestPermissions,
			"mcp_elicitations":    defaults.Granular.MCPElicitations,
		},
	}
}

// projectlessSandboxModeAllowed mirrors the `allowed_by_sandbox_mode` half of
// Rust's `permission_profile_is_allowed`
// (codex-rs/core/src/config/permission_profile_catalog.rs:92):
// a managed sandbox_mode requirement that does not admit workspace-write blocks
// the implicit defaults.
func projectlessSandboxModeAllowed(requirements *config.ConfigRequirements) bool {
	if requirements == nil || len(requirements.AllowedSandboxModes) == 0 {
		return true
	}
	for _, mode := range requirements.AllowedSandboxModes {
		if mode == sandbox.SandboxWorkspaceWrite {
			return true
		}
	}
	return false
}

// effectiveConfigValueSet reports whether key is present with a value. Go's
// config/read response materializes several required keys as null, so a nil
// entry counts as unset (Rust's `Option::is_none`).
func effectiveConfigValueSet(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	value, ok := values[key]
	return ok && value != nil
}
