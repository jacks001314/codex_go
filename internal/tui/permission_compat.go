package tui

func PermissionProfileAlias(profile string) string {
	switch profile {
	case "auto", "workspace-write":
		return ":workspace"
	case "read-only":
		return ":read-only"
	case "full-access":
		return ":danger-full-access"
	default:
		return profile
	}
}
