package systemskills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenAIDocsScriptsKeepResponseBodyFailuresInsideFallbacksLikeRust pins
// Rust #55379 for the vendored openai-docs skill: the manual downloader reads a
// successful body inside the same timeout and transport fallback as the fetch,
// leaves HEAD responses unconsumed, and the model metadata reader awaits the
// body inside its retry attempt so a truncated read retries instead of
// surfacing as a hard failure.
func TestOpenAIDocsScriptsKeepResponseBodyFailuresInsideFallbacksLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := Install(home); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	scripts := filepath.Join(CacheRoot(home), "openai-docs", "scripts")

	manual, err := os.ReadFile(filepath.Join(scripts, "fetch-codex-manual.mjs"))
	if err != nil {
		t.Fatalf("read fetch-codex-manual.mjs: %v", err)
	}
	manualText := string(manual)
	for _, want := range []string{
		"const response = await fetch(url, {",
		"if (!response.ok || method === \"HEAD\") return response;",
		"body: await response.text(),",
	} {
		if !strings.Contains(manualText, want) {
			t.Fatalf("fetch-codex-manual.mjs is missing %q", want)
		}
	}

	metadata, err := os.ReadFile(filepath.Join(scripts, "resolve-latest-model-info.cjs"))
	if err != nil {
		t.Fatalf("read resolve-latest-model-info.cjs: %v", err)
	}
	if !strings.Contains(string(metadata), "return await response.text();") {
		t.Fatalf("resolve-latest-model-info.cjs reads the body outside the retry attempt")
	}
}
