package utils

import (
	"runtime"
	"testing"
)

// Mirrors Rust platform_tests.rs::platform_metadata_preserves_missing_and_unrecognized_values.
func TestPlatformFromOSPreservesMissingAndUnknownLikeRust(t *testing.T) {
	for _, tc := range []struct {
		metadata string
		want     Platform
	}{
		{"linux", PlatformLinux},
		{"macos", PlatformMacos},
		{"windows", PlatformWindows},
		{"", PlatformUnknown},
		{"freebsd", PlatformUnknown},
		{"Windows", PlatformUnknown},
	} {
		if got := PlatformFromOS(tc.metadata); got != tc.want {
			t.Fatalf("PlatformFromOS(%q) = %s, want %s", tc.metadata, got, tc.want)
		}
	}
}

// Mirrors Rust platform_tests.rs::path_convention_is_derived_from_platform.
func TestPlatformPathConventionLikeRust(t *testing.T) {
	for _, tc := range []struct {
		platform Platform
		want     PathConvention
		ok       bool
	}{
		{PlatformLinux, ConventionPosix, true},
		{PlatformMacos, ConventionPosix, true},
		{PlatformWindows, ConventionWindows, true},
		{PlatformUnknown, "", false},
	} {
		got, ok := tc.platform.PathConvention()
		if got != tc.want || ok != tc.ok {
			t.Fatalf("%s.PathConvention() = (%q, %v), want (%q, %v)", tc.platform, got, ok, tc.want, tc.ok)
		}
	}
}

// Mirrors Rust platform_tests.rs::native_platform_matches_current_process_metadata:
// the native identity must agree with the process OS metadata (Go's runtime.GOOS
// uses "darwin" where Rust's std uses "macos").
func TestNativePlatformMatchesProcessMetadataLikeRust(t *testing.T) {
	want := PlatformUnknown
	switch runtime.GOOS {
	case "linux":
		want = PlatformLinux
	case "darwin":
		want = PlatformMacos
	case "windows":
		want = PlatformWindows
	}
	if got := NativePlatform(); got != want {
		t.Fatalf("NativePlatform() = %s, want %s for GOOS %q", got, want, runtime.GOOS)
	}
}
