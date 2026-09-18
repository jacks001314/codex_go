package model

import "testing"

// TestSelectApprovalReviewModelMatchesRust mirrors Rust
// guardian-reviewer::select_review_model: the declared override wins, the
// provider's preferred review model is used when the catalog lists it, and the
// parent model is the fallback. The reviewer prefers `low` whenever the
// selected model supports it, otherwise it keeps the model's default effort or
// the parent's selected effort (Rust #46292).
func TestSelectApprovalReviewModelMatchesRust(t *testing.T) {
	const (
		parentSlug  = "gpt-parent"
		preferredID = "codex-auto-review"
		overrideID  = "gpt-reviewer"
	)
	parentPreset := func(levels []string, defaultLevel string) ModelPreset {
		return ModelPreset{Model: parentSlug, DefaultReasoningLevel: defaultLevel, SupportedReasoningLevels: levels}
	}
	reviewPreset := func(model string, levels []string, defaultLevel string) ModelPreset {
		return ModelPreset{Model: model, DefaultReasoningLevel: defaultLevel, SupportedReasoningLevels: levels}
	}
	tests := []struct {
		name               string
		parent             *ModelInfo
		parentEffort       string
		available          []ModelPreset
		wantModel          string
		wantEffort         string
		wantCatalogHasAuto bool
		wantOverridden     bool
	}{
		{
			name:           "override listed by the catalog prefers low",
			parent:         &ModelInfo{Slug: parentSlug, AutoReviewModelOverride: overrideID},
			available:      []ModelPreset{parentPreset([]string{"high"}, "high"), reviewPreset(overrideID, []string{"low", "high"}, "high")},
			wantModel:      overrideID,
			wantEffort:     lowReasoningEffort,
			wantOverridden: true,
		},
		{
			name:           "override without low keeps the review model default",
			parent:         &ModelInfo{Slug: parentSlug, AutoReviewModelOverride: overrideID},
			available:      []ModelPreset{parentPreset([]string{"low"}, "low"), reviewPreset(overrideID, []string{"medium", "high"}, "high")},
			wantModel:      overrideID,
			wantEffort:     "high",
			wantOverridden: true,
		},
		{
			name:               "preferred review model from the catalog",
			parent:             &ModelInfo{Slug: parentSlug, SupportedReasoningLevels: []string{"high"}, DefaultReasoningLevel: "high"},
			available:          []ModelPreset{parentPreset([]string{"high"}, "high"), reviewPreset(preferredID, []string{"low", "medium"}, "medium")},
			wantModel:          preferredID,
			wantEffort:         lowReasoningEffort,
			wantCatalogHasAuto: true,
		},
		{
			name:               "preferred review model without low keeps its default",
			parent:             &ModelInfo{Slug: parentSlug},
			available:          []ModelPreset{reviewPreset(preferredID, []string{"medium"}, "medium")},
			wantModel:          preferredID,
			wantEffort:         "medium",
			wantCatalogHasAuto: true,
		},
		{
			name:         "fallback to the parent model prefers low",
			parent:       &ModelInfo{Slug: parentSlug, SupportedReasoningLevels: []string{"low", "high"}, DefaultReasoningLevel: "medium"},
			parentEffort: "high",
			available:    []ModelPreset{parentPreset([]string{"low", "high"}, "medium")},
			wantModel:    parentSlug,
			wantEffort:   lowReasoningEffort,
		},
		{
			name:         "fallback keeps the parent selected effort",
			parent:       &ModelInfo{Slug: parentSlug, SupportedReasoningLevels: []string{"medium", "high"}, DefaultReasoningLevel: "medium"},
			parentEffort: "high",
			available:    []ModelPreset{parentPreset([]string{"medium", "high"}, "medium")},
			wantModel:    parentSlug,
			wantEffort:   "high",
		},
		{
			name:       "fallback keeps the parent default when nothing is selected",
			parent:     &ModelInfo{Slug: parentSlug, SupportedReasoningLevels: []string{"medium", "high"}, DefaultReasoningLevel: "medium"},
			available:  []ModelPreset{parentPreset([]string{"medium", "high"}, "medium")},
			wantModel:  parentSlug,
			wantEffort: "medium",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := SelectApprovalReviewModel(testCase.parent, testCase.parentEffort, preferredID, testCase.available)
			if got.Model != testCase.wantModel || got.ReasoningEffort != testCase.wantEffort {
				t.Fatalf("selection = %#v, want model %q effort %q", got, testCase.wantModel, testCase.wantEffort)
			}
			if got.DefaultReviewModelID != preferredID {
				t.Fatalf("DefaultReviewModelID = %q, want %q", got.DefaultReviewModelID, preferredID)
			}
			if got.CatalogContainsAutoReview != testCase.wantCatalogHasAuto {
				t.Fatalf("CatalogContainsAutoReview = %v, want %v", got.CatalogContainsAutoReview, testCase.wantCatalogHasAuto)
			}
			if got.ModelOverridden != testCase.wantOverridden {
				t.Fatalf("ModelOverridden = %v, want %v", got.ModelOverridden, testCase.wantOverridden)
			}
			if testCase.wantOverridden && got.ModelOverride != overrideID {
				t.Fatalf("ModelOverride = %q, want %q", got.ModelOverride, overrideID)
			}
		})
	}
}

// TestSelectApprovalReviewModelWithoutCatalogMatchesRust pins the last-resort
// branch: with no catalog and no parent model the selection stays empty rather
// than inventing a reviewer.
func TestSelectApprovalReviewModelWithoutCatalogMatchesRust(t *testing.T) {
	if got := SelectApprovalReviewModel(nil, "high", "", nil); got.Model != "" || got.ReasoningEffort != "" {
		t.Fatalf("selection without a parent = %#v, want empty", got)
	}
	got := SelectApprovalReviewModel(&ModelInfo{Slug: "gpt-parent"}, "high", "", nil)
	if got.Model != "gpt-parent" || got.ReasoningEffort != "high" {
		t.Fatalf("selection = %#v, want the parent model and its selected effort", got)
	}
}
