package appserver

import (
	"testing"

	"codex_go/config"
)

// Mirrors Rust #63594: deferring image generation requires the feature flag and
// keeps direct exposure when the image_gen namespace is excluded from Code
// Mode.
func TestImageGenerationDeferExposureLikeRust(t *testing.T) {
	base := func(deferFlag bool, excluded []any) *config.Config {
		features := map[string]any{"code_mode": map[string]any{}}
		if deferFlag {
			features["defer_image_generation"] = true
		}
		if excluded != nil {
			features["code_mode"] = map[string]any{"excluded_tool_namespaces": excluded}
		}
		return &config.Config{Values: map[string]any{"features": features}}
	}
	if imageGenerationDeferExposure(nil) {
		t.Fatal("nil config deferred image generation")
	}
	if imageGenerationDeferExposure(base(false, nil)) {
		t.Fatal("the flag is off by default")
	}
	if !imageGenerationDeferExposure(base(true, nil)) {
		t.Fatal("the flag did not defer image generation")
	}
	if imageGenerationDeferExposure(base(true, []any{"image_gen"})) {
		t.Fatal("an excluded image_gen namespace must keep direct exposure")
	}
	if !imageGenerationDeferExposure(base(true, []any{"mcp__environment"})) {
		t.Fatal("an unrelated exclusion removed the deferred exposure")
	}
}
