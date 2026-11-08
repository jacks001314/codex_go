//go:build !windows && !darwin

package doctor

// desktopSecurityEnforcement has no probe on platforms without a native backend;
// Rust renders the row on Windows (wevtutil channels) and macOS (macos_security).
func desktopSecurityEnforcement(_ *desktopApp) *DoctorCheck {
	return nil
}
