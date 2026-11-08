//go:build !windows

package mxc

// NativeSupport mirrors the Windows process security environment capability
// report. Non-Windows hosts have no such API.
type NativeSupport struct {
	Available           bool
	Major               uint32
	Minor               uint32
	SupportFlags        uint64
	FileSystemDeny      bool
	FileSystemEnumerate bool
	NetworkIngress      bool
	Reason              string
}

// ProbeNativeSupport reports MXC as unavailable off Windows, matching Rust's
// `mxc_sandbox::is_available()` on non-Windows hosts.
func ProbeNativeSupport() NativeSupport {
	return NativeSupport{Reason: UnsupportedPlatformMessage}
}
