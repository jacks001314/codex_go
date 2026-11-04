package appserver

import (
	"strings"

	"codex_go/model"
	"codex_go/session"
)

// This file mirrors Rust core/src/session/model_catalog_settings.rs (#59111):
// saved chat choices stay within the latest successfully fetched provider
// catalog. Running steps keep their captured settings; changes apply to the
// next turn.

// reasoningEffortRank mirrors Rust effort_rank. Custom, ultra and persistent
// efforts have no rank, so they cannot be replaced by a ranked fallback.
func reasoningEffortRank(effort string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none":
		return 0, true
	case "minimal":
		return 1, true
	case "low":
		return 2, true
	case "medium":
		return 3, true
	case "high":
		return 4, true
	case "xhigh":
		return 5, true
	case "max":
		return 6, true
	default:
		return 0, false
	}
}

// normalizeSavedReasoningEffort mirrors Rust model_catalog_settings::normalize:
// an unsupported ranked effort becomes the highest supported level at or below
// the selection, while an unsupported ultra or persistent selection uses the
// model's supported default when one is available. Every other selection stays.
func normalizeSavedReasoningEffort(info *model.ModelInfo, effort string) (string, bool) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if info == nil || effort == "" || len(info.SupportedReasoningLevels) == 0 {
		return effort, false
	}
	for _, supported := range info.SupportedReasoningLevels {
		if strings.EqualFold(strings.TrimSpace(supported), effort) {
			return effort, false
		}
	}
	if previous, ok := reasoningEffortRank(effort); ok {
		best := ""
		bestRank := -1
		for _, supported := range info.SupportedReasoningLevels {
			rank, ok := reasoningEffortRank(supported)
			if !ok || rank > previous || rank <= bestRank {
				continue
			}
			best = strings.ToLower(strings.TrimSpace(supported))
			bestRank = rank
		}
		if best != "" {
			return best, true
		}
		return effort, false
	}
	if effort != "ultra" && effort != "persistent" {
		return effort, false
	}
	fallback := strings.ToLower(strings.TrimSpace(info.DefaultReasoningLevel))
	if fallback == "" {
		return effort, false
	}
	for _, supported := range info.SupportedReasoningLevels {
		if strings.EqualFold(strings.TrimSpace(supported), fallback) {
			return fallback, true
		}
	}
	return effort, false
}

// normalizeSavedServiceTier mirrors the Rust rule: an unsupported `priority» or
// `ultrafast» selection resets to `default», while other tiers stay untouched.
func normalizeSavedServiceTier(info *model.ModelInfo, tier string) (string, bool) {
	tier = strings.ToLower(strings.TrimSpace(tier))
	if tier != "priority" && tier != "ultrafast" {
		return tier, false
	}
	if model.SupportsServiceTier(info, tier) {
		return tier, false
	}
	return model.ServiceTierDefaultRequestValue, true
}

// normalizeChatSettingsUpdate normalizes an incoming settings write before it
// is committed and persisted (Rust update_settings_if).
func (r *RuntimeRouter) normalizeChatSettingsUpdate(params *SettingsUpdateParams) {
	if r == nil || params == nil {
		return
	}
	modelID := ""
	if params.Model != nil {
		modelID = strings.TrimSpace(*params.Model)
	}
	if modelID == "" {
		if record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(params.ThreadID)), true, false); err == nil && record != nil {
			modelID = strings.TrimSpace(record.Metadata.Model)
		}
	}
	settings := r.threadSettingsForTurn(params.ThreadID)
	if modelID == "" {
		modelID = threadSettingsModel(settings)
	}
	info := r.modelInfoForRuntime(modelID)
	if info == nil {
		return
	}
	if params.Effort != nil {
		if effort, changed := normalizeSavedReasoningEffort(info, *params.Effort); changed {
			params.Effort = stringPtrIfNotEmpty(effort)
		}
	} else if settings.Effort != nil {
		if effort, changed := normalizeSavedReasoningEffort(info, *settings.Effort); changed {
			params.Effort = stringPtrIfNotEmpty(effort)
		}
	}
	if params.ServiceTier != nil && params.ServiceTier.Set && params.ServiceTier.Value != nil {
		if tier, changed := normalizeSavedServiceTier(info, *params.ServiceTier.Value); changed {
			value := tier
			params.ServiceTier = &ThreadExtraOptionalString{Set: true, Value: &value}
		}
	}
}

// normalizeTurnStartChatSettings applies the catalog to the config overrides a
// turn is about to capture, so a stale selection never reaches the provider.
func normalizeTurnStartChatSettings(config map[string]any, info *model.ModelInfo) {
	if config == nil || info == nil {
		return
	}
	if raw, ok := config["model_reasoning_effort"].(string); ok {
		if effort, changed := normalizeSavedReasoningEffort(info, raw); changed {
			config["model_reasoning_effort"] = effort
		}
	}
	if raw, ok := config["service_tier"].(string); ok {
		if tier, changed := normalizeSavedServiceTier(info, raw); changed {
			config["service_tier"] = tier
		}
	}
}

// normalizeSavedChatSettingsForRecord applies a refreshed catalog to one saved
// thread record and reports whether anything changed.
func (r *RuntimeRouter) normalizeSavedChatSettingsForRecord(record *session.Record) bool {
	if r == nil || record == nil {
		return false
	}
	info := r.modelInfoForRuntime(strings.TrimSpace(record.Metadata.Model))
	if info == nil {
		return false
	}
	overrides := threadRecordConfigOverrides(record)
	changed := false
	if raw, ok := overrides["model_reasoning_effort"].(string); ok {
		if effort, updated := normalizeSavedReasoningEffort(info, raw); updated {
			overrides["model_reasoning_effort"] = effort
			changed = true
		}
	}
	if raw, ok := overrides["service_tier"].(string); ok {
		if tier, updated := normalizeSavedServiceTier(info, raw); updated {
			overrides["service_tier"] = tier
			record.Metadata.ServiceTier = effectiveSelectedServiceTier(r.requireModels(), tier, record.Metadata.Model)
			changed = true
		}
	}
	if !changed {
		return false
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	record.Metadata.Extra["config"] = overrides
	return true
}

// normalizeSavedChatSettingsAfterRefresh mirrors Rust
// model_catalog_settings::apply for open threads: a successful provider refresh
// corrects the saved choices of every live chat.
func (r *RuntimeRouter) normalizeSavedChatSettingsAfterRefresh() {
	if r == nil || r.threads == nil {
		return
	}
	for _, threadID := range r.threads.LiveThreadIDs() {
		record, err := r.threadRecord(threadID, true, false)
		if err != nil || record == nil {
			continue
		}
		if !r.normalizeSavedChatSettingsForRecord(record) {
			continue
		}
		_ = r.runtimeSaveThreadRecord(record)
	}
}
