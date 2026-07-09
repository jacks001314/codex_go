package tui

func VersionLine(info AppInfo) string {
	if info.Version == "" {
		return info.Name
	}
	return info.Name + " " + info.Version
}
