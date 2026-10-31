package appserver

import (
	"strings"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// skillCatalogSessionConfigForTurn returns the configuration Rust resolves the
// captured step model metadata with. core/src/session/mod.rs captures the
// session's ModelInfoOverrides when the thread is created
// (model_info_overrides: config.to_models_manager_config()), and
// StepSettings::apply_update re-resolves a step's CatalogModel from those
// session overrides rather than a turn-local Config. A turn's own
// `model_context_window` therefore changes that turn's compaction limits
// without re-budgeting the skill catalogs, which is what Rust #44202 reads
// through input.model_info.resolved_context_window().
func (r *RuntimeRouter) skillCatalogSessionConfigForTurn(cfg *config.Config, params *turn.TurnStartParams) *config.Config {
	if r == nil || params == nil {
		return cfg
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if threadID == "" {
		return cfg
	}
	record, err := r.threadRecord(session.ThreadID(threadID), false, false)
	if err != nil || record == nil {
		return cfg
	}
	sessionParams := *params
	sessionParams.Config = threadRecordConfigOverrides(record)
	sessionConfig, err := r.effectiveConfigForTurn(&sessionParams)
	if err != nil || sessionConfig == nil {
		return cfg
	}
	return sessionConfig
}
