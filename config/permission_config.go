package config

import (
	"strings"

	"codex_go/sandbox"
)

// PermissionConfig selects either a legacy sandbox mode or a named permission
// profile. It mirrors Rust PermissionConfig (#60702): the value is resolved
// from the source layers so competing selectors keep their original
// precedence, while the merged values still supply workspace-write settings.
type PermissionConfig struct {
	// Legacy reports that the selector is the legacy sandbox_mode syntax.
	Legacy bool
	// Mode is the selected legacy sandbox mode.
	Mode sandbox.SandboxMode
	// WorkspaceWrite is the merged sandbox_workspace_write table.
	WorkspaceWrite any
	// ID is the selected named profile id.
	ID string
}

// ResolvePermissionConfig mirrors Rust resolve_permission_config (#60702).
// layers must be ordered from lowest to highest precedence; values must be
// their effective merged view. A sandbox mode override wins outright, an
// explicit session-flags default_permissions retains the profile-selection
// exception, otherwise the last layer that sets either selector decides.
// A nil result means trust/platform-dependent defaults still apply.
//
// Layer values are never modified, so field origins stay accurate.
func ResolvePermissionConfig(layers []Layer, values map[string]any, sandboxModeOverride *sandbox.SandboxMode) *PermissionConfig {
	legacy := func(mode sandbox.SandboxMode) *PermissionConfig {
		return &PermissionConfig{
			Legacy:         true,
			Mode:           mode,
			WorkspaceWrite: values["sandbox_workspace_write"],
		}
	}
	if sandboxModeOverride != nil {
		return legacy(*sandboxModeOverride)
	}
	named := func() *PermissionConfig {
		id := strings.TrimSpace(stringFromConfigValue(values["default_permissions"]))
		if id == "" {
			return nil
		}
		return &PermissionConfig{ID: id}
	}
	current := named()
	// Rust: an explicit session profile selection keeps the existing override
	// exception, even when a lower layer only knows the legacy syntax.
	for index := len(layers) - 1; index >= 0; index-- {
		if layers[index].Name.Type != LayerSourceSessionFlags {
			continue
		}
		layerValues, _ := layers[index].Config.(map[string]any)
		if _, present := layerValues["default_permissions"]; present {
			return current
		}
		break
	}
	selection := (*PermissionConfig)(nil)
	for index := range layers {
		layerValues, ok := layers[index].Config.(map[string]any)
		if !ok {
			continue
		}
		if _, present := layerValues["sandbox_mode"]; present {
			selection = nil
			if modeText := strings.TrimSpace(stringFromConfigValue(values["sandbox_mode"])); modeText != "" {
				if mode, err := sandbox.ParseSandboxMode(modeText); err == nil {
					selection = legacy(mode)
				}
			}
		}
		if _, present := layerValues["default_permissions"]; present {
			selection = current
		}
	}
	if selection != nil {
		return selection
	}
	if current != nil {
		return current
	}
	if modeText := strings.TrimSpace(stringFromConfigValue(values["sandbox_mode"])); modeText != "" {
		if mode, err := sandbox.ParseSandboxMode(modeText); err == nil {
			return legacy(mode)
		}
	}
	return nil
}
