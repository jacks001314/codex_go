//go:build !windows && !darwin

package doctor

// desktopInstalledApp reports no desktop application on platforms Rust's
// platform::installed_app does not probe.
func desktopInstalledApp() (*desktopApp, error) {
	return nil, nil
}

func desktopApplicationRunning(_ *desktopApp, _ int) bool {
	return false
}
