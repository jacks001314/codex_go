//go:build !windows

package doctor

import (
	"runtime"
	"strconv"
)

// Rust doctor/system.rs reports os_info::get(). Only the Windows backend is
// reproduced at full fidelity; other hosts keep the runtime identity and leave
// the marketing version unknown, which os_info also does when it cannot detect
// the distribution.
func doctorOSIdentity() (string, string, string) {
	osType := runtime.GOOS
	name := osType
	switch osType {
	case "darwin":
		name = "macOS"
	case "linux":
		name = "Linux"
	case "freebsd":
		name = "FreeBSD"
	}
	bitness := "32-bit"
	if strconv.IntSize == 64 {
		bitness = "64-bit"
	}
	return name + " [" + bitness + "]", osType, "unknown"
}

func doctorPreferredLocale() string {
	return ""
}
