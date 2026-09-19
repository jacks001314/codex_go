package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
)

// TestUsageTagsConfiguredAndEffectiveContextAreDistinctLikeRust mirrors Rust's
// feedback_config_tests::configured_and_effective_context_are_distinct_and_unset_is_explicit:
// the configured override, its presence marker, the resolved effective window,
// and the service tier are reported separately, and an absent value is the
// explicit "unset" marker.
func TestUsageTagsConfiguredAndEffectiveContextAreDistinctLikeRust(t *testing.T) {
	window := int64(872_000)
	cfg := &config.Config{Values: map[string]any{"model_context_window": window}}
	modelInfo := model.ModelInfo{Slug: "test-model", ContextWindow: 272_000, MaxContextWindow: 400_000, EffectiveContextWindowPercent: 95}
	resolved := model.WithConfigOverrides(modelInfo, &model.ModelsManagerConfig{ModelContextWindow: window})
	tags := UsageTagsForTurn(cfg, nil, &resolved, "priority")
	for name, want := range map[string]string{
		"model_context_window":           "872000",
		"model_context_window_override":  "true",
		"model_context_window_effective": "380000",
		"service_tier":                   "priority",
	} {
		if tags[name] != want {
			t.Fatalf("%s = %q, want %q", name, tags[name], want)
		}
	}

	cfg.Values = map[string]any{}
	tags = UsageTagsForTurn(cfg, nil, &modelInfo, "")
	for name, want := range map[string]string{
		"model_context_window":           "unset",
		"model_context_window_override":  "false",
		"model_context_window_effective": "258400",
		"service_tier":                   "unset",
	} {
		if tags[name] != want {
			t.Fatalf("%s = %q, want %q", name, tags[name], want)
		}
	}
}

// TestUsageTagsReportEveryFeatureExceptCollabAndSpawnCsvLikeRust mirrors Rust's
// reported_features_have_boolean_tags_that_change_with_their_state.
func TestUsageTagsReportEveryFeatureExceptCollabAndSpawnCsvLikeRust(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{}}
	modelInfo := &model.ModelInfo{Slug: "test-model"}
	disabledSettings := map[string]bool{}
	for _, spec := range features.Registry {
		disabledSettings[spec.Key] = false
	}
	disabled := UsageTagsForTurn(cfg, disabledSettings, modelInfo, "")
	enabled := UsageTagsForTurn(cfg, featureEnablementSettings(), modelInfo, "")

	for _, spec := range features.Registry {
		key := "feature." + spec.Key
		if usageTagFeatureExclusions[spec.Key] {
			if _, present := disabled[key]; present {
				t.Fatalf("excluded feature %s was reported", spec.Key)
			}
			continue
		}
		if disabled[key] != "false" || enabled[key] != "true" {
			t.Fatalf("%s tags = %q/%q, want false/true", key, disabled[key], enabled[key])
		}
	}
}

// TestUsageTagsDocumentJSONLikeRust pins the serialized document the sampling
// span carries: sorted keys and string values.
func TestUsageTagsDocumentJSONLikeRust(t *testing.T) {
	document := UsageTagsJSON(map[string]string{"b": "two", "a": "one"})
	if document != `{"a":"one","b":"two"}` {
		t.Fatalf("document = %s", document)
	}
	if UsageTagsJSON(nil) != "" {
		t.Fatal("an empty document should be omitted")
	}
}

func featureEnablementSettings() map[string]bool {
	settings := map[string]bool{}
	for _, spec := range features.Registry {
		settings[spec.Key] = true
	}
	return settings
}
