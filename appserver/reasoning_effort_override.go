package appserver

import (
	"strings"

	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/turn"
)

// Reasoning-effort overrides (Rust #43110/#43795/#43796/#44285/#44276).
//
// With the disabled-by-default `reasoning_effort_override` feature enabled for
// an OpenAI model that uses Responses Lite, the sampling request keeps a pinned
// baseline effort for the current context window while trusted
// `configuration_update` items carry the selected effort. Only harness-authored
// items establish an override.

type requestEffortUsage int

const (
	requestEffortSampling requestEffortUsage = iota
	requestEffortCompaction
)

type reasoningEffortPinKind int

const (
	reasoningEffortPinUnset reasoningEffortPinKind = iota
	// reasoningEffortPinCompacted marks the pin retired by a successful
	// compaction, so the next sampling request re-establishes the selected
	// effort without an extra configuration update.
	reasoningEffortPinCompacted
	reasoningEffortPinActive
)

// reasoningEffortPin mirrors Rust `ReasoningEffortPin`: the original request
// effort for the current model while configuration updates remain active.
type reasoningEffortPin struct {
	kind   reasoningEffortPinKind
	model  string
	effort string
}

func (p reasoningEffortPin) get(modelSlug string) (string, bool) {
	if p.kind == reasoningEffortPinActive && p.model == modelSlug {
		return p.effort, true
	}
	return "", false
}

func (p *reasoningEffortPin) pin(modelSlug string, effort string) string {
	if pinned, ok := p.get(modelSlug); ok {
		return pinned
	}
	p.kind = reasoningEffortPinActive
	p.model = modelSlug
	p.effort = effort
	return effort
}

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
	if pin.kind == reasoningEffortPinUnset {
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
	r.setReasoningEffortPinState(threadID, reasoningEffortPin{kind: reasoningEffortPinCompacted})
}

func reasoningEffortFeatureEnabled(cfg *config.Config) bool {
	return cfg != nil && features.Enabled(cfg.FeatureSettings(), "reasoning_effort_override")
}

// effortForConfigurationUpdate mirrors Rust `Session::effort_for_configuration_update`:
// the resolved effort a trusted update may carry, gated on the feature,
// Responses Lite, an OpenAI provider, and a known (non-custom) effort.
func (r *RuntimeRouter) effortForConfigurationUpdate(cfg *config.Config, params *turn.TurnStartParams, modelInfo *model.ModelInfo, providerID string) (string, bool) {
	if r == nil || cfg == nil || modelInfo == nil || !reasoningEffortFeatureEnabled(cfg) {
		return "", false
	}
	if !modelInfo.UseResponsesLite {
		return "", false
	}
	providerInfo, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil || providerInfo == nil || !providerInfo.IsOpenAI() {
		return "", false
	}
	effective := appReasoningEffortForTurn(cfg, params)
	if effective == "" {
		effective = strings.TrimSpace(modelInfo.DefaultReasoningLevel)
	}
	if effective == "" {
		return "", false
	}
	resolved := model.ResolveReasoningEffort(modelInfo, effective)
	if resolved == "" || !model.IsKnownReasoningEffort(resolved) {
		return "", false
	}
	return resolved, true
}

// reasoningEffortOverrideInputItems mirrors Rust
// `Session::record_reasoning_effort_override`: it decides whether this turn
// appends a trusted configuration_update for the selected effort. It must be
// evaluated before the request effort is pinned for the turn.
func (r *RuntimeRouter) reasoningEffortOverrideInputItems(threadID, modelSlug, effort string, overrideAvailable bool, historyItems []any) []any {
	if r == nil || !overrideAvailable || effort == "" {
		return nil
	}
	pin := r.reasoningEffortPinState(threadID)
	if pin.kind == reasoningEffortPinCompacted {
		// Only a successful compaction retires the request baseline; the next
		// update re-activates the pin for the selected effort.
		pin.pin(modelSlug, effort)
		r.setReasoningEffortPinState(threadID, pin)
	}
	established, establishedIsTail, hasEstablished := latestTrustedReasoningUpdate(historyItems)
	// Recovery adds no user message: reuse a matching trusted tail update even
	// before this runtime establishes its pin (Rust #44276).
	if hasEstablished && establishedIsTail && established == effort {
		return nil
	}
	if pinned, ok := pin.get(modelSlug); ok {
		compare := pinned
		if hasEstablished {
			compare = established
		}
		if compare == effort {
			return nil
		}
	}
	return []any{map[string]any{
		"type":      "configuration_update",
		"reasoning": map[string]any{"effort": effort},
	}}
}

// latestTrustedReasoningUpdate returns the most recent trusted
// configuration_update effort in model-visible history, whether it is the last
// item, and whether one exists. Untrusted updates never reach this input list
// (history conversion drops them).
func latestTrustedReasoningUpdate(historyItems []any) (string, bool, bool) {
	for i := len(historyItems) - 1; i >= 0; i-- {
		payload, ok := configurationUpdatePayload(historyItems[i])
		if !ok || stringFromMap(payload, "type") != "configuration_update" {
			continue
		}
		reasoning, _ := payload["reasoning"].(map[string]any)
		effort := strings.TrimSpace(stringFromMap(reasoning, "effort"))
		if effort == "" {
			continue
		}
		return effort, i == len(historyItems)-1, true
	}
	return "", false, false
}

// reasoningEffortForRequest mirrors Rust `Session::reasoning_effort_for_request`.
// Sampling pins the selected effort for the current model; compaction reuses
// the pin when it matches and never mutates it.
func (r *RuntimeRouter) reasoningEffortForRequest(threadID, modelSlug, selectedEffort string, featureEnabled bool, overrideEffort string, overrideAvailable bool, usage requestEffortUsage) string {
	if r == nil || !featureEnabled {
		return selectedEffort
	}
	if usage == requestEffortCompaction {
		if pinned, ok := r.reasoningEffortPinState(threadID).get(modelSlug); ok {
			return pinned
		}
	}
	if !overrideAvailable {
		if usage == requestEffortSampling {
			r.clearReasoningEffortPin(threadID)
		}
		return selectedEffort
	}
	if usage == requestEffortSampling {
		pin := r.reasoningEffortPinState(threadID)
		effort := pin.pin(modelSlug, overrideEffort)
		r.setReasoningEffortPinState(threadID, pin)
		return effort
	}
	return overrideEffort
}

func reasoningEffortModelSlug(modelInfo *model.ModelInfo, fallback string) string {
	if modelInfo != nil && strings.TrimSpace(modelInfo.Slug) != "" {
		return strings.TrimSpace(modelInfo.Slug)
	}
	return strings.TrimSpace(fallback)
}

func isConfigurationUpdateInputItem(item any) bool {
	payload, ok := configurationUpdatePayload(item)
	return ok && stringFromMap(payload, "type") == "configuration_update"
}
