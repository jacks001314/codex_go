//go:build !darwin

package remotecontrol

// detectHostDeviceKind reports no device kind on platforms other than macOS:
// Rust omits the x-codex-host-device-kind header when detection is
// unavailable (#38840).
func detectHostDeviceKind() (string, bool) {
	return "", true
}
