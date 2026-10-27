package appserver

import (
	"encoding/json"
	"strings"

	"codex_go/model"
	"codex_go/rollout"
	"codex_go/session"
)

// persistedModelSettings holds the model selections a cold resume restores
// (Rust #55923): the selected service tier, reasoning summary, and personality.
type persistedModelSettings struct {
	ServiceTier      *string
	ReasoningSummary *string
	Personality      *string
}

// latestPersistedModelSettings loads the thread-owned model selections from
// rollout history. It mirrors Rust's latest_persisted_model_settings: an
// ownerless legacy snapshot is accepted until a foreign snapshot is crossed,
// and TurnContext entries never supply model settings.
func (r *Router) latestPersistedModelSettings(record *session.Record) (rollout.PersistedModelSettings, bool) {
	if r == nil || record == nil {
		return rollout.PersistedModelSettings{}, false
	}
	path := r.threadRolloutPath(record)
	if strings.TrimSpace(path) == "" {
		return rollout.PersistedModelSettings{}, false
	}
	if _, ok := rollout.ExistingRolloutPath(path); !ok {
		return rollout.PersistedModelSettings{}, false
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		return rollout.PersistedModelSettings{}, false
	}
	return rollout.LatestPersistedModelSettings(string(record.ID), lines)
}

// restoreColdResumeModelSettings applies the persisted model selections that a
// cold resume restores to record, honoring explicit request overrides, and
// returns the selections the resumed thread should use. The record's config
// overrides are updated so the checkpoint written by resume and every later
// turn carry the selected values even before a turn runs.
func (r *Router) restoreColdResumeModelSettings(record *session.Record, params *ThreadResumeParams) (persistedModelSettings, bool) {
	if r == nil || record == nil {
		return persistedModelSettings{}, false
	}
	restored, _ := r.latestPersistedModelSettings(record)
	selected := persistedModelSettings{}
	previousTier := threadRecordSelectedServiceTier(record)
	previousMetadataTier := strings.TrimSpace(record.Metadata.ServiceTier)
	previousSummary := threadRecordReasoningSummary(record)
	previousPersonality := threadRecordPersonality(record)
	overrides := threadRecordConfigOverrides(record)
	if overrides == nil {
		overrides = map[string]any{}
	}
	config := map[string]any(nil)
	if params != nil {
		config = params.Config
	}

	if params != nil && (params.ServiceTierSet || params.ServiceTier != nil) {
		if value := threadLifecycleServiceTier(params.ServiceTierSet, params.ServiceTier); strings.TrimSpace(value) != "" {
			selected.ServiceTier = modelSettingPtr(value)
		}
	} else if value, ok := threadConfigStringValue(config, "service_tier"); ok {
		selected.ServiceTier = modelSettingPtr(value)
	} else if restored.ServiceTier != nil {
		selected.ServiceTier = cloneString(restored.ServiceTier)
	}

	if value, ok := threadConfigStringValue(config, "model_reasoning_summary"); ok {
		selected.ReasoningSummary = modelSettingPtr(value)
	} else if restored.ReasoningSummary != nil {
		selected.ReasoningSummary = cloneString(restored.ReasoningSummary)
	}

	if params != nil && params.Personality != nil {
		if value := strings.TrimSpace(*params.Personality); value != "" {
			selected.Personality = modelSettingPtr(value)
		} else {
			delete(overrides, "personality")
		}
	} else if value, ok := threadConfigStringValue(config, "personality"); ok {
		selected.Personality = modelSettingPtr(value)
	} else if restored.Personality != nil {
		selected.Personality = cloneString(restored.Personality)
	}

	if selected.ServiceTier != nil {
		overrides["service_tier"] = *selected.ServiceTier
		record.Metadata.ServiceTier = *selected.ServiceTier
	}
	if selected.ReasoningSummary != nil {
		overrides["model_reasoning_summary"] = *selected.ReasoningSummary
	}
	if selected.Personality != nil {
		overrides["personality"] = *selected.Personality
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	if len(overrides) > 0 {
		record.Metadata.Extra["config"] = overrides
	}
	changed := !equalOptionalSettings(previousTier, selected.ServiceTier) ||
		previousMetadataTier != strings.TrimSpace(record.Metadata.ServiceTier) ||
		!equalOptionalSettings(previousSummary, selected.ReasoningSummary) ||
		!equalOptionalSettings(previousPersonality, selected.Personality)
	return selected, changed
}

func equalOptionalSettings(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func modelSettingPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

// threadConfigStringValue returns a trimmed string config override and reports
// whether the key was present with a non-empty value.
func threadConfigStringValue(values map[string]any, key string) (string, bool) {
	if len(values) == 0 {
		return "", false
	}
	raw, ok := values[key]
	if !ok {
		return "", false
	}
	switch value := raw.(type) {
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return "", false
		}
		return value, true
	case json.Number:
		return strings.TrimSpace(value.String()), true
	default:
		text := strings.TrimSpace(stringFromAny(value))
		if text == "" {
			return "", false
		}
		return text, true
	}
}

// threadRecordSelectedServiceTier returns the requested service tier recorded
// for a thread. The settings snapshot keeps the requested value rather than the
// model-filtered effective value (Rust #55923).
func threadRecordSelectedServiceTier(record *session.Record) *string {
	if value, ok := threadConfigStringValue(threadRecordConfigOverrides(record), "service_tier"); ok {
		return modelSettingPtr(value)
	}
	if record != nil {
		if value := strings.TrimSpace(record.Metadata.ServiceTier); value != "" {
			return modelSettingPtr(value)
		}
	}
	return nil
}

func threadRecordReasoningSummary(record *session.Record) *string {
	if value, ok := threadConfigStringValue(threadRecordConfigOverrides(record), "model_reasoning_summary"); ok {
		return modelSettingPtr(value)
	}
	return nil
}

func threadRecordPersonality(record *session.Record) *string {
	if value, ok := threadConfigStringValue(threadRecordConfigOverrides(record), "personality"); ok {
		return modelSettingPtr(value)
	}
	return nil
}

// settingsSelectedServiceTier reports the tier selected by a live settings
// snapshot, falling back to the thread record before the first settings write.
func settingsSelectedServiceTier(settings *Settings, record *session.Record) *string {
	if settings != nil && settings.ServiceTier != nil {
		return cloneString(settings.ServiceTier)
	}
	return threadRecordSelectedServiceTier(record)
}

func settingsReasoningSummary(settings *Settings, record *session.Record) *string {
	if settings != nil && settings.Summary != nil {
		return cloneString(settings.Summary)
	}
	return threadRecordReasoningSummary(record)
}

func settingsPersonality(settings *Settings, record *session.Record) *string {
	if settings != nil && settings.Personality != nil {
		return cloneString(settings.Personality)
	}
	return threadRecordPersonality(record)
}

// effectiveSelectedServiceTier filters a selected tier against the model
// catalog while preserving flex, matching Rust get_service_tier (#55923).
func effectiveSelectedServiceTier(service *model.ModelService, requested string, modelID string) string {
	return threadLifecycleServiceTierForRequest(service, requested, modelID)
}
