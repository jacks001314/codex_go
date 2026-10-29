package app

import (
	"strings"

	"codex_go/cli"
	"codex_go/config"
)

// remoteResumePermissionOverrideMessage and remoteForkPermissionOverrideMessage
// mirror Rust #43330 (codex-rs/tui/src/app/config_persistence.rs and startup.rs):
// a remote task keeps the permissions saved on its server, so an explicit
// launch override is refused instead of silently replacing them.
const (
	remoteResumePermissionOverrideMessage = "Permission overrides are not supported when resuming a remote task."
	remoteForkPermissionOverrideMessage   = "Permission overrides are not supported when forking a remote task."
)

// remotePermissionOverrideKeys mirrors Rust's
// has_explicit_resume_permission_override key list. A winning value for any of
// these keys in a session-flags layer or a profile-scoped user layer counts as
// an explicit launch permission selection.
var remotePermissionOverrideKeys = []string{
	"approval_policy",
	"approvals_reviewer",
	"sandbox_mode",
	"default_permissions",
	"permissions",
	"network",
	"sandbox_workspace_write",
}

// remotePermissionOverrideCLIKeys are the generic `-c` paths that Rust's
// ConfigOverrides check treats as permission selections, including the root
// lists Rust compares separately.
var remotePermissionOverrideCLIKeys = []string{
	"approval_policy",
	"approvals_reviewer",
	"sandbox_mode",
	"permission_profile",
	"default_permissions",
	"additional_writable_roots",
	"workspace_roots",
	"permissions",
	"network",
	"sandbox_workspace_write",
}

// hasExplicitRemotePermissionOverride reports whether this launch explicitly
// selected permissions and therefore cannot inherit a remote task's saved
// server settings (Rust #43330 has_explicit_resume_permission_override).
func hasExplicitRemotePermissionOverride(root *cli.RootOptions) bool {
	if root == nil {
		return false
	}
	if strings.TrimSpace(root.Shared.Sandbox) != "" ||
		strings.TrimSpace(root.Shared.ApprovalPolicy) != "" ||
		root.Shared.DangerouslyBypassApprovalsAndSandbox ||
		len(root.Shared.AddDirs) > 0 {
		return true
	}
	for _, key := range remoteCLIConfigOverrideKeys(root) {
		key = strings.TrimSpace(key)
		for _, candidate := range remotePermissionOverrideCLIKeys {
			if key == candidate || strings.HasPrefix(key, candidate+".") {
				return true
			}
		}
	}
	read, err := interactiveConfigService(root).Read(&config.ConfigReadParams{IncludeLayers: true})
	if err != nil || read == nil {
		return false
	}
	return layersCarryRemotePermissionOverride(read.Layers)
}

// layersCarryRemotePermissionOverride is the layer-stack half of Rust's
// explicit-permission check: only session flags and profile-scoped user layers
// are launch selections; a project or plain user config value is not.
func layersCarryRemotePermissionOverride(layers []config.Layer) bool {
	for _, layer := range layers {
		launch := layer.Name.Type == config.LayerSourceSessionFlags ||
			(layer.Name.Type == config.LayerSourceUser && layer.Name.Profile != nil)
		if !launch {
			continue
		}
		values, ok := layer.Config.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range remotePermissionOverrideKeys {
			if _, present := nestedConfigValue(values, key); present {
				return true
			}
		}
	}
	return false
}
