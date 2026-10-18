package chatwidget

// PermissionPresetDescription returns the concise built-in picker copy shared
// by the permission menus (Rust #47329 permission_preset_description).
func PermissionPresetDescription(preset ApprovalPreset) string {
	switch preset.ID {
	case "read-only":
		return "Read workspace files, with approval required for edits or internet access"
	case "auto":
		return "Read and edit workspace files and run commands, with approval required for internet access or edits outside the workspace"
	case "full-access":
		return "Use with caution: Codex can edit files outside this workspace and access the internet without approval"
	default:
		return preset.Description
	}
}

func NewPermissionsMenu(config PermissionMenuConfig) PermissionMenuView {
	return NewPermissionsPopupView(config)
}

func NewPermissionProfilesMenu(config PermissionMenuConfig) PermissionMenuView {
	return NewPermissionProfilesPopupView(config)
}
