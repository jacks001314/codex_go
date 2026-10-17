package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
)

func TestModelTokenBudgetDefaultsActivateThreadSnapshotLikeRust(t *testing.T) {
	info := &model.ModelInfo{ModelMessages: &model.ModelMessages{TokenBudget: &model.ModelTokenBudgetConfig{
		Enabled:                         true,
		UseHistoryNotesExtension:        true,
		ReminderThresholdTokens:         12000,
		ReminderMessageTemplate:         "remaining: {n_remaining}",
		GuidanceMessage:                 "keep notes",
		AutoCompactFallbackPrompt:       "summarize",
		AutoCompactFallbackBufferTokens: 8000,
	}}}
	snapshot := map[string]any{}
	applyModelTokenBudgetThreadDefaults(snapshot, &config.Config{Values: map[string]any{}}, info)

	snapshotCfg := &config.Config{Values: snapshot}
	if !features.Enabled(snapshotCfg.FeatureSettings(), "token_budget") {
		t.Fatalf("thread snapshot did not enable token_budget: %#v", snapshot)
	}
	tokenBudget, err := snapshotCfg.TokenBudgetConfigWithDefaults(modelTokenBudgetDefaults(info))
	if err != nil {
		t.Fatal(err)
	}
	if !tokenBudget.Enabled || !tokenBudget.UseHistoryNotesExtension ||
		tokenBudget.ReminderThresholdTokens == nil || *tokenBudget.ReminderThresholdTokens != 12000 ||
		tokenBudget.GuidanceMessage != "keep notes" {
		t.Fatalf("thread token budget = %+v", tokenBudget)
	}
}

func TestModelTokenBudgetDefaultsRespectExplicitAndPinnedConfigLikeRust(t *testing.T) {
	info := &model.ModelInfo{ModelMessages: &model.ModelMessages{TokenBudget: &model.ModelTokenBudgetConfig{
		Enabled:                         true,
		ReminderThresholdTokens:         12000,
		ReminderMessageTemplate:         "remaining: {n_remaining}",
		GuidanceMessage:                 "keep notes",
		AutoCompactFallbackPrompt:       "summarize",
		AutoCompactFallbackBufferTokens: 8000,
	}}}
	for _, test := range []struct {
		name string
		cfg  *config.Config
	}{
		{
			name: "explicit disabled",
			cfg: &config.Config{Values: map[string]any{
				"features": map[string]any{"token_budget": false},
			}},
		},
		{
			name: "requirements pin off",
			cfg: &config.Config{
				Values: map[string]any{},
				Requirements: &config.ConfigRequirements{
					FeatureRequirements: map[string]bool{"token_budget": false},
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := map[string]any{}
			applyModelTokenBudgetThreadDefaults(snapshot, test.cfg, info)
			if _, present := snapshot["features"]; present {
				t.Fatalf("defaults changed an explicitly controlled config: %#v", snapshot)
			}
		})
	}
}
