package model

import (
	"encoding/json"
	"testing"
)

// TestBundledModelsCatalogMatchesRustSnapshot pins the catalog the binary
// ships: Rust embeds models-manager/models.json and `debug models` prints the
// serde form, so the Go snapshot must stay a valid ModelsResponse that the
// offline fallback can serve.
func TestBundledModelsCatalogMatchesRustSnapshot(t *testing.T) {
	raw := BundledModelsCatalogJSON()
	if len(raw) == 0 {
		t.Fatal("bundled catalog snapshot is empty")
	}
	if !json.Valid(raw) {
		t.Fatal("bundled catalog snapshot is not valid JSON")
	}
	var response ModelsResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("Unmarshal bundled catalog error = %v", err)
	}
	if len(response.Models) != 12 {
		t.Fatalf("bundled catalog has %d models, want the 0.166 release's 12", len(response.Models))
	}
	bySlug := map[string]ModelInfo{}
	for _, info := range response.Models {
		bySlug[info.Slug] = info
	}
	for _, slug := range []string{
		"gpt-6-astra",
		"gpt-6.1-sol",
		"gpt-daybreak-blue-latest",
		"gpt-daybreak-red-latest",
		"gpt-5.5",
		"codex-auto-review",
	} {
		if _, ok := bySlug[slug]; !ok {
			t.Fatalf("bundled catalog is missing %s", slug)
		}
	}
	// The catalog stores reasoning levels as {effort, description} objects;
	// the runtime keeps the ordered effort list.
	if got := bySlug["gpt-6.1-sol"].SupportedReasoningLevels; len(got) != 6 || got[0] != "low" || got[5] != "ultra" {
		t.Fatalf("gpt-6.1-sol reasoning levels = %#v", got)
	}
	embedded, err := loadEmbeddedModelsResponse()
	if err != nil {
		t.Fatalf("loadEmbeddedModelsResponse() error = %v", err)
	}
	if len(embedded.Models) != len(response.Models) {
		t.Fatalf("embedded catalog has %d models, want %d", len(embedded.Models), len(response.Models))
	}
}
