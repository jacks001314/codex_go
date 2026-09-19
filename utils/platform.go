package utils

import "runtime"

// Platform is the operating system whose paths and execution configuration are
// being resolved. It mirrors Rust codex-utils-path-uri Platform (#46334): the
// identity is carried explicitly so a controller can validate configuration for
// an executor that runs a different OS, and missing or unrecognized metadata is
// preserved as PlatformUnknown rather than substituted with the local host.
type Platform int

const (
	PlatformUnknown Platform = iota
	PlatformLinux
	PlatformMacos
	PlatformWindows
)

// String returns the platform identity using the same spelling as Rust's
// `Platform` Debug output, which is embedded in validation error messages.
func (p Platform) String() string {
	switch p {
	case PlatformLinux:
		return "Linux"
	case PlatformMacos:
		return "Macos"
	case PlatformWindows:
		return "Windows"
	default:
		return "Unknown"
	}
}

// PlatformFromOS reads platform metadata without substituting the current
// host's platform. Missing or unrecognized values carry no path convention.
func PlatformFromOS(platformOS string) Platform {
	switch platformOS {
	case "linux":
		return PlatformLinux
	case "macos":
		return PlatformMacos
	case "windows":
		return PlatformWindows
	default:
		return PlatformUnknown
	}
}

// NativePlatform returns the platform of the current process.
func NativePlatform() Platform {
	switch runtime.GOOS {
	case "linux":
		return PlatformLinux
	case "darwin":
		return PlatformMacos
	case "windows":
		return PlatformWindows
	default:
		return PlatformUnknown
	}
}

// PathConvention derives the path grammar from platform identity while
// preserving unknown metadata (Rust Platform::path_convention).
func (p Platform) PathConvention() (PathConvention, bool) {
	switch p {
	case PlatformLinux, PlatformMacos:
		return ConventionPosix, true
	case PlatformWindows:
		return ConventionWindows, true
	default:
		return "", false
	}
}
