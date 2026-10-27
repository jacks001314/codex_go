package execserver

import (
	"strings"

	"golang.org/x/mod/semver"
)

// minCapabilityDiscoveryV2ExecutorVersion is the first stable executor release
// with the final DiscoveryV2 contract (Rust #57192).
const minCapabilityDiscoveryV2ExecutorVersion = "v0.164.0"

// SupportsCapabilityDiscoveryV2 reports whether an executor advertises V2
// discovery and is new enough to implement the final contract. Missing or
// malformed versions are incompatible, as are prereleases of the cutoff
// release; a newer prerelease (for example 0.165.0-alpha.1) is compatible.
func SupportsCapabilityDiscoveryV2(info *EnvironmentInfo) bool {
	if info == nil || !info.Capabilities.CapabilityDiscoveryV2 {
		return false
	}
	version := strings.TrimSpace(info.ExecutorVersion)
	if !isRustSemanticVersion(version) {
		return false
	}
	return semver.Compare("v"+version, minCapabilityDiscoveryV2ExecutorVersion) >= 0
}

// isRustSemanticVersion reports whether version parses with Rust's
// semver::Version::parse, which requires the full `major.minor.patch` form and
// rejects the `v` prefix and the shorthand forms x/mod/semver accepts.
func isRustSemanticVersion(version string) bool {
	if version == "" || strings.HasPrefix(version, "v") {
		return false
	}
	if !semver.IsValid("v" + version) {
		return false
	}
	core := version
	if i := strings.IndexByte(core, '+'); i >= 0 {
		core = core[:i]
	}
	if i := strings.IndexByte(core, '-'); i >= 0 {
		core = core[:i]
	}
	return strings.Count(core, ".") == 2
}
