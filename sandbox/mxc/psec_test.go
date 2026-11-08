package mxc

import (
	"runtime"
	"testing"
)

// TestProbeNativeSupportMatchesPlatform freezes the contract the executor
// diagnostic depends on: the probe is cached, an unavailable host always says
// why, and an available host reports the MXC-required PSEC version plus the
// capability bits that gate deny paths / ingress policy.
func TestProbeNativeSupportMatchesPlatform(t *testing.T) {
	first := ProbeNativeSupport()
	second := ProbeNativeSupport()
	if first != second {
		t.Fatalf("ProbeNativeSupport() not cached: %#v vs %#v", first, second)
	}
	if runtime.GOOS != "windows" && first.Available {
		t.Fatalf("non-Windows probe reported available: %#v", first)
	}
	if first.Available && first.Reason != "" {
		t.Fatalf("available probe carries a reason: %#v", first)
	}
	if !first.Available && first.Reason == "" {
		t.Fatalf("unavailable probe carries no reason: %#v", first)
	}
	if first.Available && first.Major != 1 {
		t.Fatalf("available probe reported PSEC v%d, want v1", first.Major)
	}
	if first.FileSystemDeny != (first.SupportFlags&psecSupportFileSystemDeny != 0) ||
		first.FileSystemEnumerate != (first.SupportFlags&psecSupportFileSystemEnumerate != 0) ||
		first.NetworkIngress != (first.SupportFlags&psecSupportNetworkIngress != 0) {
		t.Fatalf("capability bits do not match support flags: %#v", first)
	}
}
