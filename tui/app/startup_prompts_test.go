package app

import (
	"path/filepath"
	"reflect"
	"testing"

	"codex_go/appserver"
	"codex_go/model"
)

func TestSelectModelAvailabilityNUXMatchRust(t *testing.T) {
	first := model.ModelSummary{
		ID:              "gpt-5",
		Model:           "gpt-5",
		AvailabilityNux: &model.ModelAvailabilityNux{Message: "new model"},
	}
	second := model.ModelSummary{
		ID:              "gpt-5.1",
		Model:           "gpt-5.1",
		AvailabilityNux: &model.ModelAvailabilityNux{Message: "newer model"},
	}

	got, ok := SelectModelAvailabilityNUX([]model.ModelSummary{first, second}, map[string]uint32{"gpt-5": ModelAvailabilityNUXMaxShowCount})
	if !ok {
		t.Fatal("SelectModelAvailabilityNUX() ok = false, want true")
	}
	if got != (StartupTooltipOverride{ModelSlug: "gpt-5.1", Message: "newer model"}) {
		t.Fatalf("SelectModelAvailabilityNUX() = %#v", got)
	}

	if _, ok := SelectModelAvailabilityNUX([]model.ModelSummary{first}, map[string]uint32{"gpt-5": ModelAvailabilityNUXMaxShowCount}); ok {
		t.Fatal("SelectModelAvailabilityNUX(max shown) ok = true, want false")
	}
	if _, ok := SelectModelAvailabilityNUX([]model.ModelSummary{{ID: "gpt-5", Model: "gpt-5"}}, nil); ok {
		t.Fatal("SelectModelAvailabilityNUX(no nux) ok = true, want false")
	}
}

func TestSelectModelAvailabilityNUXFallsBackToID(t *testing.T) {
	got, ok := SelectModelAvailabilityNUX([]model.ModelSummary{{
		ID:              "gpt-id",
		AvailabilityNux: &model.ModelAvailabilityNux{Message: "message"},
	}}, nil)
	if !ok {
		t.Fatal("SelectModelAvailabilityNUX() ok = false, want true")
	}
	if got.ModelSlug != "gpt-id" || got.Message != "message" {
		t.Fatalf("SelectModelAvailabilityNUX() = %#v", got)
	}
}

func TestPrepareStartupTooltipOverrideDecisionMatchRust(t *testing.T) {
	models := []model.ModelSummary{{
		ID:              "gpt-5.1",
		Model:           "gpt-5.1",
		AvailabilityNux: &model.ModelAvailabilityNux{Message: "new"},
	}}
	if decision := PrepareStartupTooltipOverrideDecision(true, true, models, nil); decision.Show {
		t.Fatalf("first run decision = %#v", decision)
	}
	if decision := PrepareStartupTooltipOverrideDecision(false, false, models, nil); decision.Show {
		t.Fatalf("tooltips disabled decision = %#v", decision)
	}
	decision := PrepareStartupTooltipOverrideDecision(false, true, models, map[string]uint32{"gpt-5.1": 2})
	if !decision.Show || decision.Override.ModelSlug != "gpt-5.1" || decision.UpdatedShownCount["gpt-5.1"] != 3 {
		t.Fatalf("tooltip decision = %#v", decision)
	}
}

func TestModelMigrationPromptDecisionMatchRust(t *testing.T) {
	target := "gpt-5.1"
	models := []model.ModelSummary{
		{ID: "gpt-5", Model: "gpt-5", Upgrade: &target},
		{ID: "gpt-5.1", Model: "gpt-5.1", DefaultReasoningEffort: "high"},
	}
	if !ShouldShowModelMigrationPrompt("openai", "gpt-5", "gpt-5.1", nil, models) {
		t.Fatal("expected migration prompt")
	}
	if ShouldShowModelMigrationPrompt("openai", "gpt-5.1", "gpt-5.1", nil, models) {
		t.Fatal("same model should not prompt")
	}
	if ShouldShowModelMigrationPrompt("openai", "gpt-5", "gpt-5.1", map[string]string{"gpt-5": "gpt-5.1"}, models) {
		t.Fatal("seen migration should not prompt")
	}
	hiddenTarget := []model.ModelSummary{
		{ID: "gpt-5", Model: "gpt-5", Upgrade: &target},
		{ID: "gpt-5.1", Model: "gpt-5.1", Hidden: true},
	}
	if ShouldShowModelMigrationPrompt("openai", "gpt-5", "gpt-5.1", nil, hiddenTarget) {
		t.Fatal("hidden target should not prompt")
	}
	viaOtherPreset := []model.ModelSummary{
		{ID: "legacy", Model: "legacy"},
		{ID: "other", Model: "other", Upgrade: &target},
		{ID: "gpt-5.1", Model: "gpt-5.1"},
	}
	if !ShouldShowModelMigrationPrompt("openai", "legacy", "gpt-5.1", nil, viaOtherPreset) {
		t.Fatal("upgrade target referenced by any preset should prompt")
	}
}

func TestModelUpgradeForMigrationScopesFallbackToProviderLikeRust(t *testing.T) {
	// Rust #47932: a saved selection that outlived its catalog entry keeps its
	// migration metadata, but the fallback is scoped to the owning provider
	// because other providers may still support the same slug.
	cases := []struct {
		name         string
		providerID   string
		model        string
		wantID       string
		wantMarkdown string
		wantOK       bool
	}{
		{
			name:         "openai gpt-5.4",
			providerID:   model.OpenAIProviderID,
			model:        "gpt-5.4",
			wantID:       "gpt-6-sol",
			wantMarkdown: "GPT-5.4 is no longer available\n\nCodex now uses GPT-6 Sol in place of GPT-5.4. Switch to GPT-6 Sol to continue.\n",
			wantOK:       true,
		},
		{
			name:         "bedrock openai.gpt-5.4",
			providerID:   model.AmazonBedrockProviderID,
			model:        "openai.gpt-5.4",
			wantID:       "openai.gpt-6-sol",
			wantMarkdown: "GPT-5.4 on Amazon Bedrock is no longer offered in Codex\n\nCodex now uses GPT-6 Sol on Amazon Bedrock in place of GPT-5.4 on Amazon Bedrock. Switch to GPT-6 Sol on Amazon Bedrock to continue.\n",
			wantOK:       true,
		},
		{
			name:         "openai gpt-5.4-mini",
			providerID:   model.OpenAIProviderID,
			model:        "gpt-5.4-mini",
			wantID:       "gpt-6-luna",
			wantMarkdown: "GPT-5.4 Mini is no longer available\n\nCodex now uses GPT-6 Luna in place of GPT-5.4 Mini. Switch to GPT-6 Luna to continue.\n",
			wantOK:       true,
		},
		{
			name:       "bedrock does not migrate the OpenAI gpt-5.4 slug",
			providerID: model.AmazonBedrockProviderID,
			model:      "gpt-5.4",
			wantOK:     false,
		},
		{
			name:       "openai does not migrate the Bedrock openai.gpt-5.4 slug",
			providerID: model.OpenAIProviderID,
			model:      "openai.gpt-5.4",
			wantOK:     false,
		},
	}
	for _, tc := range cases {
		upgrade, ok := ModelUpgradeForMigration(tc.providerID, tc.model, nil)
		if ok != tc.wantOK {
			t.Fatalf("%s: ok = %v, want %v", tc.name, ok, tc.wantOK)
		}
		if !ok {
			continue
		}
		if upgrade.ID != tc.wantID {
			t.Fatalf("%s: upgrade.ID = %q, want %q", tc.name, upgrade.ID, tc.wantID)
		}
		if upgrade.MigrationConfigKey != tc.model {
			t.Fatalf("%s: migration config key = %q, want %q", tc.name, upgrade.MigrationConfigKey, tc.model)
		}
		if upgrade.MigrationMarkdown == nil || *upgrade.MigrationMarkdown != tc.wantMarkdown {
			t.Fatalf("%s: markdown = %v, want %q", tc.name, upgrade.MigrationMarkdown, tc.wantMarkdown)
		}
	}
}

func TestModelUpgradeForMigrationPrefersCatalogMetadataLikeRust(t *testing.T) {
	target := "gpt-5.6-luna"
	markdown := "custom migration markdown"
	models := []model.ModelSummary{{
		ID:          "gpt-5.4",
		Model:       "gpt-5.4",
		Upgrade:     &target,
		UpgradeInfo: &model.ModelUpgradeInfo{Model: target, MigrationMarkdown: &markdown},
	}}
	upgrade, ok := ModelUpgradeForMigration(model.OpenAIProviderID, "gpt-5.4", models)
	if !ok {
		t.Fatal("catalog preset upgrade should win over the fallback")
	}
	if upgrade.ID != target || upgrade.MigrationMarkdown == nil || *upgrade.MigrationMarkdown != markdown {
		t.Fatalf("upgrade = %#v", upgrade)
	}
	// A catalog preset without an upgrade yields no migration metadata, exactly
	// like Rust's `preset.upgrade.clone()`.
	if _, ok := ModelUpgradeForMigration(model.OpenAIProviderID, "gpt-5.4", []model.ModelSummary{{ID: "gpt-5.4", Model: "gpt-5.4"}}); ok {
		t.Fatal("preset without upgrade should not migrate")
	}
}

func TestModelMigrationPromptUsesProviderScopedFallbackLikeRust(t *testing.T) {
	models := []model.ModelSummary{{ID: "gpt-6-sol", Model: "gpt-6-sol"}}
	if !ShouldShowModelMigrationPrompt(model.OpenAIProviderID, "gpt-5.4", "gpt-6-sol", nil, models) {
		t.Fatal("a saved gpt-5.4 selection should prompt for the OpenAI fallback target")
	}
	if ShouldShowModelMigrationPrompt(model.AmazonBedrockProviderID, "gpt-5.4", "gpt-6-sol", nil, models) {
		t.Fatal("the fallback must not apply to a provider that does not own the slug")
	}
	if ShouldShowModelMigrationPrompt(model.OpenAIProviderID, "gpt-5.4", "gpt-6-luna", nil, models) {
		t.Fatal("the fallback must not prompt for an unrelated target")
	}
}

func TestMigrationPromptHiddenTargetPresetAndAcceptedActionsMatchRust(t *testing.T) {
	if !MigrationPromptHidden(map[string]bool{HideGPT51MigrationPromptConfig: true}, HideGPT51MigrationPromptConfig) {
		t.Fatal("migration prompt hidden key should hide")
	}
	if MigrationPromptHidden(map[string]bool{"unknown": true}, "unknown") {
		t.Fatal("unknown hidden key should not hide")
	}

	target, ok := TargetPresetForUpgrade([]model.ModelSummary{
		{ID: "hidden", Model: "gpt-hidden", Hidden: true},
		{ID: "target", Model: "gpt-target", DefaultReasoningEffort: "medium"},
	}, "gpt-target")
	if !ok || target.ID != "target" {
		t.Fatalf("target preset = %#v ok=%v", target, ok)
	}
	if _, ok := TargetPresetForUpgrade([]model.ModelSummary{{ID: "hidden", Model: "gpt-hidden", Hidden: true}}, "gpt-hidden"); ok {
		t.Fatal("hidden target should not be selected")
	}

	actions := ApplyAcceptedModelMigrationActions("gpt-old", target)
	if actions.FromModel != "gpt-old" || actions.TargetModel != "gpt-target" || actions.TargetReasoningEffort != "medium" || !actions.PersistAcknowledgement || !actions.UpdateModel || !actions.UpdateReasoningEffort || !actions.PersistModelSelection {
		t.Fatalf("actions = %#v", actions)
	}
}

func TestProjectConfigWarningAndHarnessOverrideNormalizationMatchRust(t *testing.T) {
	warnings := BuildProjectConfigWarningMessages([]ProjectConfigDisabledFolder{
		{Folder: "/repo/.gcode", Reason: "not trusted"},
		{Folder: "/repo/sub/.gcode", Reason: "owner mismatch"},
	})
	want := []string{"Project-local config, hooks, and exec policies are disabled in the following folders until the project is trusted, but skills still load.\n    1. /repo/.gcode\n       not trusted\n    2. /repo/sub/.gcode\n       owner mismatch"}
	if !reflect.DeepEqual(warnings, want) {
		t.Fatalf("warnings = %#v, want %#v", warnings, want)
	}
	if got := BuildProjectConfigWarningMessages(nil); got != nil {
		t.Fatalf("empty warnings = %#v, want nil", got)
	}

	base := t.TempDir()
	normalized := NormalizeAdditionalWritableRootsForCWD([]string{"rel", "", filepath.Join(base, "abs")}, base)
	if len(normalized) != 2 || normalized[0] != filepath.Clean(filepath.Join(base, "rel")) {
		t.Fatalf("normalized = %#v", normalized)
	}
}

func startupSkillError(path string, message string) appserver.SkillErrorInfo {
	return appserver.SkillErrorInfo{Path: path, Message: message}
}
