package reasoningoverride

import (
	"testing"

	"codex_go/model"
)

func providerCatalogInfo(levels ...string) *model.ModelInfo {
	return &model.ModelInfo{Slug: "gpt-narrow", SupportedReasoningLevels: levels}
}

// TestPinDisallowedByModelLikeRust pins Rust #59111: only a captured provider
// catalog that no longer represents the pinned effort retires the pin.
func TestPinDisallowedByModelLikeRust(t *testing.T) {
	info := providerCatalogInfo("low", "medium")
	if !PinDisallowedByModel(info, model.ModelOriginProvider, "xhigh") {
		t.Fatal("a provider catalog with narrower levels must disallow the pin")
	}
	if PinDisallowedByModel(info, model.ModelOriginProvider, "medium") {
		t.Fatal("a supported pin must stay")
	}
	if PinDisallowedByModel(info, model.ModelOriginBundled, "xhigh") {
		t.Fatal("bundled metadata makes no claim about the pin")
	}
	if PinDisallowedByModel(&model.ModelInfo{Slug: "gpt"}, model.ModelOriginProvider, "xhigh") {
		t.Fatal("a catalog without levels makes no claim about the pin")
	}
	if PinDisallowedByModel(nil, model.ModelOriginProvider, "xhigh") {
		t.Fatal("nil metadata makes no claim about the pin")
	}
}

// TestRequestEffortRetiresDisallowedPinLikeRust pins the sampling and
// compaction behavior: sampling replaces the baseline, compaction ignores the
// disallowed pin without mutating the live one.
func TestRequestEffortRetiresDisallowedPinLikeRust(t *testing.T) {
	info := providerCatalogInfo("low", "medium")
	pin := Pin{Kind: PinActive, Model: "gpt-narrow", Effort: "xhigh"}

	effort, sampled := RequestEffortForModel(info, model.ModelOriginProvider, pin, "gpt-narrow", "medium", true, "medium", true, UsageSampling)
	if effort != "medium" || sampled.Kind != PinActive || sampled.Effort != "medium" {
		t.Fatalf("sampling = %q/%#v, want the new baseline", effort, sampled)
	}

	effort, compacted := RequestEffortForModel(info, model.ModelOriginProvider, pin, "gpt-narrow", "medium", true, "medium", true, UsageCompaction)
	if effort != "medium" {
		t.Fatalf("compaction effort = %q, want the selected effort", effort)
	}
	if compacted != pin {
		t.Fatalf("compaction mutated the live pin: %#v", compacted)
	}

	allowed := Pin{Kind: PinActive, Model: "gpt-narrow", Effort: "low"}
	keptEffort, kept := RequestEffortForModel(info, model.ModelOriginProvider, allowed, "gpt-narrow", "medium", true, "medium", true, UsageCompaction)
	if keptEffort != "low" || kept != allowed {
		t.Fatalf("allowed compaction = %q/%#v, want the pinned baseline", keptEffort, kept)
	}
}
