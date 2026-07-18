package chatwidget

import "testing"

func TestWarningDisplayStateDeduplicatesFallbackModelMetadataWarnings(t *testing.T) {
	var state WarningDisplayState
	warning := "Model metadata for `gpt-test` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."
	if !state.ShouldDisplay(warning) {
		t.Fatal("first warning should display")
	}
	if state.ShouldDisplay(warning) {
		t.Fatal("duplicate fallback model metadata warning should not display")
	}
	other := "Model metadata for `gpt-other` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."
	if !state.ShouldDisplay(other) {
		t.Fatal("different fallback model metadata warning should display")
	}
	if !state.ShouldDisplay("plain warning") || !state.ShouldDisplay("plain warning") {
		t.Fatal("plain warnings should not be deduplicated")
	}
}

func TestFallbackModelMetadataWarningSlug(t *testing.T) {
	got, ok := FallbackModelMetadataWarningSlug("Model metadata for `gpt-test` not found. Defaulting to fallback metadata; this can degrade performance and cause issues.")
	if !ok || got != "gpt-test" {
		t.Fatalf("slug = %q ok=%v", got, ok)
	}
	if got, ok := FallbackModelMetadataWarningSlug(" Model metadata for `gpt-test` not found. Defaulting to fallback metadata; this can degrade performance and cause issues. "); ok || got != "" {
		t.Fatalf("spaced warning should not match Rust exact prefix/suffix: slug = %q ok=%v", got, ok)
	}
	emptySlug := "Model metadata for `" + fallbackModelMetadataWarningSuffix
	if got, ok := FallbackModelMetadataWarningSlug(emptySlug); !ok || got != "" {
		t.Fatalf("empty slug should still match like Rust: slug = %q ok=%v", got, ok)
	}
	if got, ok := FallbackModelMetadataWarningSlug("Model metadata missing"); ok || got != "" {
		t.Fatalf("non fallback slug = %q ok=%v", got, ok)
	}
}
