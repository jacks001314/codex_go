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

// effortForConfigurationUpdate mirrors Rust `Session::effort_for_configuration_update`:
// the resolved effort a trusted update may carry, gated on the feature,
// Responses Lite, an OpenAI provider, and a known (non-custom) effort.
func (r *RuntimeRouter) effortForConfigurationUpdate(cfg *config.Config, params *turn.TurnStartParams, modelInfo *model.ModelInfo, providerID string) (string, bool) {
	if r == nil || cfg == nil || modelInfo == nil {
		return "", false
	}
	providerOpenAI := false
	if providerInfo, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url")); err == nil && providerInfo != nil && providerInfo.IsOpenAI() {
		providerOpenAI = true
	}
	return reasoningoverride.EffortForConfigurationUpdate(
		reasoningEffortFeatureEnabled(cfg),
		providerOpenAI,
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
