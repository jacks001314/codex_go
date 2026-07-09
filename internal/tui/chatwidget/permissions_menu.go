package chatwidget

func NewPermissionsMenu(config PermissionMenuConfig) PermissionMenuView {
	return NewPermissionsPopupView(config)
}

func NewPermissionProfilesMenu(config PermissionMenuConfig) PermissionMenuView {
	return NewPermissionProfilesPopupView(config)
}
