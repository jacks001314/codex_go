package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/state"
)

func guardianProviderParentConfig() *config.Config {
	return &config.Config{Values: map[string]any{
		"model":                                "actor-model",
		"service_tier":                         "priority",
		"model_catalog_json":                   "catalog.json",
		"model_context_window":                 400000,
		"model_auto_compact_token_limit":       300000,
		"model_auto_compact_token_limit_scope": "total",
		"tool_output_token_limit":              20000,
		"model_verbosity":                      "high",
		"compact_prompt":                       "custom compact prompt",
		"features": map[string]any{
			"async_compaction": map[string]any{
				"enabled":                               true,
				"model_async_compact_threshold_percent": 40,
			},
			"guardianv2": map[string]any{"transcript_mode": "json"},
		},
	}}
}

// Mirrors Rust #62588 GuardianInferenceContext::configure_reviewer: a reviewer
// bound to the host-selected Guardian provider starts from its own inference
// settings, so the actor's tier, catalog, context, compaction and transcript
// choices do not carry over.
func TestGuardianProviderReviewerDropsActorRuntimeOverridesLikeRust(t *testing.T) {
	input := guardianReviewerPrepareInput{
		ParentConfig:     guardianProviderParentConfig(),
		ParentModel:      "actor-model",
		ReviewerModel:    "actor-model",
		BaseInstructions: "reviewer policy",
		HostProvider:     true,
	}
	prepared, err := guardianPrepareReviewerOptions(input)
	if err != nil {
		t.Fatalf("guardianPrepareReviewerOptions() error = %v", err)
	}
	values := prepared.Config.Values
	for _, key := range []string{
		"service_tier",
		"model_catalog_json",
		"model_context_window",
		"model_auto_compact_token_limit",
		"model_auto_compact_token_limit_scope",
		"tool_output_token_limit",
		"model_verbosity",
		"compact_prompt",
	} {
		if _, ok := values[key]; ok {
			t.Fatalf("actor %s survived into the Guardian reviewer configuration", key)
		}
	}
	features, _ := values["features"].(map[string]any)
	if asyncCompaction, ok := features["async_compaction"].(map[string]any); ok {
		if _, kept := asyncCompaction["model_async_compact_threshold_percent"]; kept {
			t.Fatal("actor async compaction threshold survived into the Guardian reviewer configuration")
		}
	}
	if guardianV2, ok := features["guardianv2"].(map[string]any); ok {
		if _, kept := guardianV2["transcript_mode"]; kept {
			t.Fatal("actor transcript mode survived into the Guardian reviewer configuration")
		}
	}

	// Without a host provider the inherited reviewer keeps its runtime settings.
	inherited := input
	inherited.HostProvider = false
	inheritedPrepared, err := guardianPrepareReviewerOptions(inherited)
	if err != nil {
		t.Fatalf("guardianPrepareReviewerOptions() error = %v", err)
	}
	if _, ok := inheritedPrepared.Config.Values["service_tier"]; !ok {
		t.Fatal("an inherited reviewer must keep the actor's service tier")
	}
	if _, ok := inheritedPrepared.Config.Values["model_context_window"]; !ok {
		t.Fatal("an inherited reviewer with the same model must keep the actor's context window")
	}
}

// Mirrors Rust #62588 Config::guardian_reuses_parent_compaction: a host-selected
// Guardian provider keeps Guardian's history independent, so the actor's
// opaque compaction checkpoints are never reused.
func TestGuardianProviderDisablesParentCompactionReuseLikeRust(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{}}
	actorRouter := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	if !actorRouter.guardianReusesParentCompaction(cfg) {
		t.Fatal("the default feature must let an inherited reviewer reuse parent compaction")
	}
	if got := actorRouter.guardianContextModeForConfig(cfg); got != state.GuardianContextModeThreadOwned {
		t.Fatalf("actor context mode = %q, want thread-owned", got)
	}

	guardianRouter := NewRuntimeRouter(RuntimeServices{
		Config:                config.NewConfigService(t.TempDir()),
		GuardianModelProvider: &GuardianInferenceProvider{ID: "guardian", Info: model.ProviderInfo{EnvKey: "GUARDIAN_API_KEY"}},
	})
	if guardianRouter.guardianReusesParentCompaction(cfg) {
		t.Fatal("a host-selected Guardian provider must not reuse actor compaction")
	}
	if got := guardianRouter.guardianContextModeForConfig(cfg); got != state.GuardianContextModeIndependent {
		t.Fatalf("Guardian context mode = %q, want independent", got)
	}
}

// Mirrors Rust #62588: only a host-selected Guardian provider is reported on
// analytics events.
func TestGuardianModelProviderIDForAnalyticsLikeRust(t *testing.T) {
	actorRouter := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	if id := actorRouter.guardianModelProviderIDPtr(); id != nil {
		t.Fatalf("actor Guardian analytics provider = %q, want none", *id)
	}
	guardianRouter := NewRuntimeRouter(RuntimeServices{
		Config:                config.NewConfigService(t.TempDir()),
		GuardianModelProvider: &GuardianInferenceProvider{ID: "guardian-provider", Info: model.ProviderInfo{EnvKey: "K"}},
	})
	id := guardianRouter.guardianModelProviderIDPtr()
	if id == nil || *id != "guardian-provider" {
		t.Fatalf("Guardian analytics provider = %v, want guardian-provider", id)
	}
}
