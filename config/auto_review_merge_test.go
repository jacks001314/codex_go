package config

import (
	"reflect"
	"testing"
)

// TestMergeAutoReviewRequirementsLayersLikeRust mirrors Rust 208f05b233 and
// 2e3a1702c2: required_on_models unions across requirement layers while
// ignore_rules follows first-wins semantics.
func TestMergeAutoReviewRequirementsLayersLikeRust(t *testing.T) {
	base := &ConfigRequirements{
		AutoReview: &AutoReviewRequirements{
			RequiredOnModels: []string{"managed-a"},
			IgnoreRules:      []string{"ignored-a"},
		},
	}
	overlay := &ConfigRequirements{
		AutoReview: &AutoReviewRequirements{
			RequiredOnModels: []string{"managed-b", "managed-a"},
			IgnoreRules:      []string{"ignored-b"},
		},
	}
	merged := mergeConfigRequirements(base, overlay)
	if merged == nil || merged.AutoReview == nil {
		t.Fatalf("merged = %#v", merged)
	}
	if got := merged.AutoReview.RequiredOnModels; !reflect.DeepEqual(got, []string{"managed-a", "managed-b"}) {
		t.Fatalf("RequiredOnModels = %#v, want union [managed-a managed-b]", got)
	}
	if got := merged.AutoReview.IgnoreRules; !reflect.DeepEqual(got, []string{"ignored-a"}) {
		t.Fatalf("IgnoreRules = %#v, want first-wins [ignored-a]", got)
	}

	// When the base layer has no ignore rules, the overlay contributes them.
	baseOnly := &ConfigRequirements{AutoReview: &AutoReviewRequirements{RequiredOnModels: []string{"managed-a"}}}
	merged = mergeConfigRequirements(baseOnly, overlay)
	if got := merged.AutoReview.IgnoreRules; !reflect.DeepEqual(got, []string{"ignored-b"}) {
		t.Fatalf("IgnoreRules = %#v, want overlay [ignored-b]", got)
	}
}
