package features

import "testing"

// TestDeferImageGenerationRegisteredLikeRust pins Rust #63594: code mode can
// discover the image generation tool through ALL_TOOLS while the initial model
// request omits it. Go registers the under-development flag, off by default.
func TestDeferImageGenerationRegisteredLikeRust(t *testing.T) {
	const key = "defer_image_generation"
	if !Known(key) {
		t.Fatalf("%s should be known", key)
	}
	if got := StageFor(key); got != StageUnderDevelopment {
		t.Fatalf("StageFor(%s) = %q, want %q", key, got, StageUnderDevelopment)
	}
	if defaults := Defaults(); defaults[key] {
		t.Fatalf("%s default = true, want false", key)
	}
}
