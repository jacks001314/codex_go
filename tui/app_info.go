package tui

type AppInfo struct {
	Name    string
	Version string
}

func DefaultAppInfo(version string) AppInfo {
	return AppInfo{Name: "Codex", Version: version}
}
