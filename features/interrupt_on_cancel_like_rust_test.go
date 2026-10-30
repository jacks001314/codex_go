package features

import "testing"

// Rust #57864: interrupting upstream inference on cancellation is an
// under-development feature that is off by default.
func TestInterruptOnCancelFeatureRegisteredLikeRust(t *testing.T) {
	const key = "interrupt_on_cancel"
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
