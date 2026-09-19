package appserver

import (
	"strings"

	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/reasoningoverride"
	"codex_go/turn"
)

// Reasoning-effort overrides (Rust #43110/#43795/#43796/#44285/#44276).
//
// With the disabled-by-default `reasoning_effort_override` feature enabled for
// an OpenAI model that uses Responses Lite, the sampling request keeps a pinned
// baseline effort for the current context window while trusted
// `configuration_update` items carry the selected effort. Only harness-authored
// items establish an override. The pin and decision logic live in
// `reasoningoverride` so the app-server and exec entry points share it.

type requestEffortUsage = reasoningoverride.Usage

const (
	requestEffortSampling   = reasoningoverride.UsageSampling
	requestEffortCompaction = reasoningoverride.UsageCompaction
)

// reasoningEffortPin aliases the shared pin so RuntimeRouter's per-thread map
// keeps its existing shape.
type reasoningEffortPin = reasoningoverride.Pin

const (
	reasoningEffortPinUnset     = reasoningoverride.PinUnset
	reasoningEffortPinCompacted = reasoningoverride.PinCompacted
	reasoningEffortPinActive    = reasoningoverride.PinActive
)

func (r *RuntimeRouter) reasoningEffortPinState(threadID string) reasoningEffortPin {
	r.reasoningEffortMu.Lock()
	defer r.reasoningEffortMu.Unlock()
	if r.reasoningEffortPins == nil {
		return reasoningEffortPin{}
	}
	return r.reasoningEffortPins[threadID]
}

func (r *RuntimeRouter) setReasoningEffortPinState(threadID string, pin reasoningEffortPin) {
	r.reasoningEffortMu.Lock()
	defer r.reasoningEffortMu.Unlock()
	if r.reasoningEffortPins == nil {
		r.reasoningEffortPins = map[string]reasoningEffortPin{}
	}
	if pin.Kind == reasoningEffortPinUnset {
		delete(r.reasoningEffortPins, threadID)
		return
	}
	r.reasoningEffortPins[threadID] = pin
}

// clearReasoningEffortPin mirrors Rust `state.reasoning_effort_pin = Unset`
// after rollback, resume, or a model switch: the next send re-establishes the
// currently selected effort.
func (r *RuntimeRouter) clearReasoningEffortPin(threadID string) {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	r.setReasoningEffortPinState(threadID, reasoningEffortPin{})
}

// markReasoningEffortPinCompacted retires the pin after a successful
// compaction (Rust #43796) so the next sampling request establishes the
// selected effort as its baseline.
func (r *RuntimeRouter) markReasoningEffortPinCompacted(threadID string) {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	r.setReasoningEffortPinState(threadID, reasoningEffortPin{Kind: reasoningEffortPinCompacted})
}

func reasoningEffortFeatureEnabled(cfg *config.Config) bool {
	return cfg != nil && features.Enabled(cfg.FeatureSettings(), "reasoning_effort_override")
}

// reasoningEffortProviderIsOpenAI resolves whether the turn's provider is the
// OpenAI provider, which the override gate requires (#46530).
func reasoningEffortProviderIsOpenAI(cfg *config.Config, providerID string) bool {
	if cfg == nil {
		return false
	}
	providerInfo, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	return err == nil && providerInfo != nil && providerInfo.IsOpenAI()
}

// reasoningEffortOverrideExempt mirrors Rust #46531: fixed-effort workers
// (memory consolidation and ephemeral `thread_title` generation) use their
// selected request-level effort even when managed settings enable the override
// feature. Persisted `thread_title` threads and other ephemeral threads keep
// the normal override behavior.
func (r *RuntimeRouter) reasoningEffortOverrideExempt(threadID string) bool {
	if r == nil {
		return false
	}
	record := r.runtimeRecordForThread(threadID)
	if record == nil {
		return false
	}
	threadSource := strings.ToLower(strings.TrimSpace(record.Metadata.ThreadSource))
	source := strings.ToLower(strings.TrimSpace(record.Metadata.Source))
	if threadSource == string(ThreadSourceMemoryConsolidation) || strings.Contains(source, "memory_consolidation") {
		return true
	}
	if runtimeRecordEphemeral(record) && threadSource == "thread_title" {
		return true
	}
	return false
}

// reasoningEffortOverrideEnabled mirrors Rust
// `ModelClient::reasoning_effort_override_enabled`: the feature, an OpenAI
// provider, explicit model support, and a non-exempt session source.
func (r *RuntimeRouter) reasoningEffortOverrideEnabled(threadID string, cfg *config.Config, modelInfo *model.ModelInfo, providerID string) bool {
	featureEnabled := reasoningEffortFeatureEnabled(cfg) && !r.reasoningEffortOverrideExempt(threadID)
	return reasoningoverride.OverrideEnabled(featureEnabled, reasoningEffortProviderIsOpenAI(cfg, providerID), modelInfo)
}

// effortForConfigurationUpdate mirrors Rust `Session::effort_for_configuration_update`:
// the resolved effort a trusted update may carry, gated on the combined
// override decision and a known (non-custom) effort.
func (r *RuntimeRouter) effortForConfigurationUpdate(threadID string, cfg *config.Config, params *turn.TurnStartParams, modelInfo *model.ModelInfo, providerID string) (string, bool) {
	if r == nil || cfg == nil || modelInfo == nil {
		return "", false
	}
	return reasoningoverride.EffortForConfigurationUpdate(
		r.reasoningEffortOverrideEnabled(threadID, cfg, modelInfo, providerID),
		modelInfo,
		appReasoningEffortForTurn(cfg, params),
	)
}

// reasoningEffortOverrideInputItems mirrors Rust
// `Session::record_reasoning_effort_override`: it decides whether this turn
// appends a trusted configuration_update for the selected effort. It must be
// evaluated before the request effort is pinned for the turn.
func (r *RuntimeRouter) reasoningEffortOverrideInputItems(threadID, modelSlug, effort string, overrideAvailable bool, historyItems []any) []any {
	if r == nil {
		return nil
	}
	items, pin := reasoningoverride.OverrideInputItems(
		r.reasoningEffortPinState(threadID),
		modelSlug,
		effort,
		overrideAvailable,
		historyItems,
	)
	r.setReasoningEffortPinState(threadID, pin)
	return items
}

// reasoningEffortForRequest mirrors Rust `Session::reasoning_effort_for_request`.
// Sampling pins the selected effort for the current model; compaction reuses
// the pin when it matches and never mutates it.
func (r *RuntimeRouter) reasoningEffortForRequest(threadID, modelSlug, selectedEffort string, featureEnabled bool, overrideEffort string, overrideAvailable bool, usage requestEffortUsage) string {
	if r == nil {
		return selectedEffort
	}
	effort, pin := reasoningoverride.RequestEffort(
		r.reasoningEffortPinState(threadID),
		modelSlug,
		selectedEffort,
		featureEnabled,
		overrideEffort,
		overrideAvailable,
		usage,
	)
	r.setReasoningEffortPinState(threadID, pin)
	return effort
}

func reasoningEffortModelSlug(modelInfo *model.ModelInfo, fallback string) string {
	return reasoningoverride.ModelSlug(modelInfo, fallback)
}

func isConfigurationUpdateInputItem(item any) bool {
	payload, ok := configurationUpdatePayload(item)
	return ok && stringFromMap(payload, "type") == "configuration_update"
}
