package model

import (
	"strings"
	"testing"
)

// Rust #62525: a per-request override must not rewrite the process-wide
// bundled catalog that later lookups read.
func TestBundledModelsManagerKeepsSharedCatalogAcrossOverridesLikeRust(t *testing.T) {
	manager := NewBundledModelsManager()
	models := manager.GetRemoteModels()
	if len(models) == 0 {
		t.Fatal("bundled catalog is empty")
	}
	bundled := models[0]
	if strings.TrimSpace(bundled.Slug) == "" {
		t.Fatal("bundled catalog has no slug")
	}

	override := "Instructions for this request only"
	overridden := manager.GetCatalogModel(bundled.Slug, &ModelsManagerConfig{BaseInstructions: &override})
	if overridden.Origin != ModelOriginBundled {
		t.Fatalf("origin = %q, want bundled", overridden.Origin)
	}
	if overridden.Model.BaseInstructions != override {
		t.Fatalf("override not applied: %q", overridden.Model.BaseInstructions)
	}
	if overridden.Model.ModelMessages == nil || overridden.Model.ModelMessages.InstructionsTemplate != override {
		t.Fatalf("instructions template = %+v, want the override", overridden.Model.ModelMessages)
	}

	original := manager.GetCatalogModel(bundled.Slug, &ModelsManagerConfig{})
	if original.Model.BaseInstructions != bundled.BaseInstructions {
		t.Fatalf("shared catalog changed: base instructions = %q, want %q", original.Model.BaseInstructions, bundled.BaseInstructions)
	}
	if original.Model.ModelMessages != nil && original.Model.ModelMessages.InstructionsTemplate == override {
		t.Fatal("shared catalog kept the request override")
	}

	// A second manager over the same parse still sees the untouched catalog.
	fresh := NewBundledModelsManager().GetCatalogModel(bundled.Slug, nil)
	if fresh.Model.BaseInstructions != bundled.BaseInstructions {
		t.Fatalf("second manager base instructions = %q, want %q", fresh.Model.BaseInstructions, bundled.BaseInstructions)
	}
}

// Rust #62525: the embedded catalog is parsed once per process.
func TestEmbeddedModelsCatalogIsParsedOnceLikeRust(t *testing.T) {
	first, ok := sharedEmbeddedModelsCatalog()
	if !ok {
		t.Fatal("shared embedded catalog unavailable")
	}
	second, ok := sharedEmbeddedModelsCatalog()
	if !ok {
		t.Fatal("shared embedded catalog unavailable on the second read")
	}
	if len(first.Models) != len(second.Models) {
		t.Fatalf("model counts differ: %d then %d", len(first.Models), len(second.Models))
	}
	for index := range first.Models {
		if first.Models[index].Slug != second.Models[index].Slug {
			t.Fatalf("shared catalog is not stable at index %d", index)
		}
	}
	// Reusing the parsed entries proves the embedded JSON was not parsed again.
	if &first.Models[0] != &second.Models[0] {
		t.Fatal("embedded catalog was parsed more than once")
	}
}
