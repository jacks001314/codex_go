package appserver

import (
	"testing"

	"codex_go/config"
)

// TestGuardianV2AnalyticsEnablementLikeRust mirrors Rust #42144: turn analytics
// report guardian_v2_enabled only when the feature is on and managed
// requirements do not force auto-review for the turn's model.
func TestGuardianV2AnalyticsEnablementLikeRust(t *testing.T) {
	enabled := &config.Config{Values: map[string]any{"features": map[string]any{"guardianv2": true}}}
	disabled := &config.Config{Values: map[string]any{"features": map[string]any{"guardianv2": false}}}

	if !guardianV2AnalyticsEnabled(enabled, "gpt-5") {
		t.Fatal("guardian v2 should report enabled without requirements")
	}
	if guardianV2AnalyticsEnabled(disabled, "gpt-5") {
		t.Fatal("guardian v2 reported enabled while the feature is off")
	}
	if guardianV2AnalyticsEnabled(nil, "gpt-5") {
		t.Fatal("nil config reported guardian v2 enabled")
	}

	required := &config.Config{
		Values: map[string]any{"features": map[string]any{"guardianv2": true}},
		Requirements: &config.ConfigRequirements{
			AutoReview: &config.AutoReviewRequirements{RequiredOnModels: []string{"gpt-5"}},
		},
	}
	if guardianV2AnalyticsEnabled(required, "gpt-5") {
		t.Fatal("auto-review requirement should disable guardian v2 analytics")
	}
	if !guardianV2AnalyticsEnabled(required, "gpt-5-other") {
		t.Fatal("unlisted model should keep guardian v2 analytics enabled")
	}
}
