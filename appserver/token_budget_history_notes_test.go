package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/model"
)

func TestValidateTokenBudgetHistoryNotesModelLikeRust(t *testing.T) {
	const wantMessage = "features.token_budget.use_history_notes_extension is not supported by model `gpt-unsupported`; disable it or select a model that supports experimental context"

	extension := &config.TokenBudgetConfig{Enabled: true, UseHistoryNotesExtension: true}
	noExtension := &config.TokenBudgetConfig{Enabled: true}
	unsupported := &model.ModelInfo{}
	supported := &model.ModelInfo{SupportsExperimentalContext: true}

	cases := []struct {
		name        string
		modelID     string
		info        *model.ModelInfo
		tokenBudget *config.TokenBudgetConfig
		wantErr     bool
	}{
		{name: "nil token budget is ignored", modelID: "gpt-unsupported", info: unsupported, tokenBudget: nil},
		{name: "extension disabled is ignored", modelID: "gpt-unsupported", info: unsupported, tokenBudget: noExtension},
		{name: "supported model activates", modelID: "gpt-supported", info: supported, tokenBudget: extension},
		{name: "unsupported model rejects", modelID: "gpt-unsupported", info: unsupported, tokenBudget: extension, wantErr: true},
		{name: "nil model info rejects", modelID: "gpt-unsupported", info: nil, tokenBudget: extension, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTokenBudgetHistoryNotesModel(tc.modelID, tc.info, tc.tokenBudget)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("validateTokenBudgetHistoryNotesModel() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("validateTokenBudgetHistoryNotesModel() error = nil, want rejection")
			}
			if err.Error() != wantMessage {
				t.Fatalf("error = %q, want %q", err.Error(), wantMessage)
			}
		})
	}
}
