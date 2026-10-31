package exec

import (
	"testing"

	"codex_go/model"
)

func TestFallbackModelMetadataWarningMatchesRust(t *testing.T) {
	message, ok := fallbackModelMetadataWarning("deepseek-v4-flash", model.ModelInfo{
		Slug:                      "deepseek-v4-flash",
		UsedFallbackModelMetadata: true,
	})
	if !ok {
		t.Fatal("fallback model did not produce a warning")
	}
	want := "Model metadata for `deepseek-v4-flash` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}

	if _, ok := fallbackModelMetadataWarning("gpt-5.1-codex", model.ModelInfo{Slug: "gpt-5.1-codex"}); ok {
		t.Fatal("catalog model produced a fallback warning")
	}
	if _, ok := fallbackModelMetadataWarning("", model.ModelInfo{UsedFallbackModelMetadata: true}); ok {
		t.Fatal("empty model id produced a fallback warning")
	}
}
