package appserver

import (
	"fmt"
	"strings"

	"codex_go/config"
	"codex_go/model"
)

// validateTokenBudgetHistoryNotesModel mirrors Rust #44883
// (core/src/session/mod.rs): after startup configuration and model defaults are
// resolved, `features.token_budget.use_history_notes_extension` is rejected for
// a starting model that does not advertise `supports_experimental_context`.
//
// Omitted model metadata defaults to false, so an unresolved model is also
// rejected. The context-management activation path never trips this check: it
// forces the extension on only when the model already supports experimental
// context.
func validateTokenBudgetHistoryNotesModel(modelID string, info *model.ModelInfo, tokenBudget *config.TokenBudgetConfig) error {
	if tokenBudget == nil || !tokenBudget.UseHistoryNotesExtension {
		return nil
	}
	if info != nil && info.SupportsExperimentalContext {
		return nil
	}
	return fmt.Errorf(
		"features.token_budget.use_history_notes_extension is not supported by model `%s`; disable it or select a model that supports experimental context",
		strings.TrimSpace(modelID),
	)
}
