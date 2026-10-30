package features

import "testing"

// Rust #58352: host-owned Apps catalog reads may overlap the initial tool
// inventory behind an under-development flag that is off by default.
func TestAppsEarlyResourceDiscoveryFeatureRegisteredLikeRust(t *testing.T) {
	const key = "apps_early_resource_discovery"
	if !Known(key) {
		t.Fatalf("%s should be known", key)
	}
	if got := StageFor(key); got != StageUnderDevelopment {
		t.Fatalf("StageFor(%s) = %q, want %q", key, got, StageUnderDevelopment)
	}
	if Defaults()[key] {
		t.Fatalf("%s default = true, want false", key)
	}
}
