package mxc

import (
	"math"
	"testing"
)

// Mirrors mxc-sdk `job_object.rs` tests: the build-gated capability map omits
// JOB_OBJECT_UILIMIT_IME below 22621 and JOB_OBJECT_UILIMIT_INJECTION below
// 26100, and treats an unknown build as modern.
func TestSupportedUILimitMaskForBuildLikeRust(t *testing.T) {
	for _, test := range []struct {
		name  string
		build uint32
		want  uint32
	}{
		{name: "server-2022", build: 20348, want: 0x00FF},
		{name: "before-ime-boundary", build: 22620, want: 0x00FF},
		{name: "win11-22h2", build: 22621, want: 0x01FF},
		{name: "win11-23h2", build: 22631, want: 0x01FF},
		{name: "before-injection-boundary", build: 26099, want: 0x01FF},
		{name: "win11-24h2", build: 26100, want: 0x03FF},
		{name: "unknown-build", build: math.MaxUint32, want: 0x03FF},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := SupportedUILimitMaskForBuild(test.build); got != test.want {
				t.Fatalf("SupportedUILimitMaskForBuild(%d) = %#04x, want %#04x", test.build, got, test.want)
			}
		})
	}
}

// Pins ALL_DEFINED_UI_LIMITS to this package's encoder, the invariant the SDK
// keeps with `encoder_known_bit_positions`.
func TestDefaultUIPolicyEncodesEveryDefinedUILimit(t *testing.T) {
	if got := EffectiveUIRestrictionMask(UIPolicy{}, BaseProcessUI{}); got != uint64(allDefinedUILimits) {
		t.Fatalf("default policy mask = %#04x, want %#04x", got, allDefinedUILimits)
	}
}

// The applied mask never exceeds what the host supports, and an already
// supported mask round-trips unchanged.
func TestEffectiveJobUILimitMaskIntersectsWithSupported(t *testing.T) {
	supported := SupportedUILimitMask()
	if got := EffectiveJobUILimitMask(uint64(allDefinedUILimits)); got != supported {
		t.Fatalf("EffectiveJobUILimitMask(all) = %#04x, want supported %#04x", got, supported)
	}
	if got := EffectiveJobUILimitMask(uint64(jobUIlimitReadClipboard)); got != uint32(jobUIlimitReadClipboard) {
		t.Fatalf("EffectiveJobUILimitMask(clipboard read) = %#04x, want %#04x", got, jobUIlimitReadClipboard)
	}
	if got := EffectiveJobUILimitMask(0); got != 0 {
		t.Fatalf("EffectiveJobUILimitMask(0) = %#04x, want 0", got)
	}
}

// A lower OS build must drop exactly the two gated flags from whatever the
// encoder produced, never more.
func TestSupportedUILimitMaskKeepsUniversalFlags(t *testing.T) {
	universal := uint32(allDefinedUILimits) &^ (uint32(jobUIlimitIME) | uint32(jobUIlimitInjection))
	if got := SupportedUILimitMaskForBuild(10240); got != universal {
		t.Fatalf("SupportedUILimitMaskForBuild(10240) = %#04x, want %#04x", got, universal)
	}
}
