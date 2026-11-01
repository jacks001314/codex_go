//go:build !windows

package doctor

// desktopSecurityEnforcement has no non-Windows probe yet; Rust renders the row
// on Windows (wevtutil channels) and macOS (macos_security).
func desktopSecurityEnforcement() *DoctorCheck {
	return nil
}
