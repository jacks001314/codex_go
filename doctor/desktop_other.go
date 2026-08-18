//go:build !windows && !darwin

package doctor

// desktopAppInstallations reports no desktop application installation on
// unsupported platforms (Rust desktop.rs, #39060).
func desktopAppInstallations() []desktopAppInstallation {
	return nil
}
