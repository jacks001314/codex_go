// Package reasoningoverride implements the runtime core of the
// reasoning-effort override feature (Rust #43110/#43795/#43796/#44285/#44276):
// a per-thread request-effort pin plus the trusted configuration-update
// decision. Callers own persistence and request construction.
package reasoningoverride

import (
	"encoding/json"
	"strings"

	"codex_go/model"
)

// Usage selects the request path that resolves an effort.
type Usage int

const (
	// UsageSampling pins the selected effort for the current model.
	UsageSampling Usage = iota
	// UsageCompaction reuses the pin when it matches and never mutates it.
	UsageCompaction
)

// PinKind distinguishes the request-baseline states.
type PinKind int

const (
	// PinUnset means no baseline has been established.
	PinUnset PinKind = iota
	// PinCompacted marks the pin retired by a successful compaction so the next
	// sampling request re-establishes the selected effort without an extra
	// configuration update.
	PinCompacted
	// PinActive holds the original request effort for one model.
	PinActive
)

// Pin mirrors Rust `ReasoningEffortPin`: the original request effort for the
// current model while configuration updates remain active.
type Pin struct {
	Kind   PinKind
	Model  string
	Effort string
}

// Get returns the pinned effort when it belongs to modelSlug.
func (p Pin) Get(modelSlug string) (string, bool) {
	if p.Kind == PinActive && p.Model == modelSlug {
		return p.Effort, true
	}
	return "", false
}

// Pin sets the baseline for modelSlug, keeping an existing matching pin.
func (p *Pin) Pin(modelSlug string, effort string) string {
	if pinned, ok := p.Get(modelSlug); ok {
		return pinned
	}
	p.Kind = PinActive
	p.Model = modelSlug
	p.Effort = effort
	return effort
}

// OverrideEnabled mirrors Rust
// `ModelClient::reasoning_effort_override_enabled`: reasoning-effort
// configuration updates require the feature, an OpenAI provider, and explicit
// model support (`supports_reasoning_effort_updates`). Responses Lite alone
// does not establish that a model accepts update items (#46530).
func OverrideEnabled(featureEnabled bool, providerIsOpenAI bool, info *model.ModelInfo) bool {
	return featureEnabled && providerIsOpenAI && info != nil && info.SupportsReasoningEffortUpdates
}

// EffortForConfigurationUpdate mirrors Rust
// `Session::effort_for_configuration_update`: the resolved effort a trusted
// update may carry, gated on the combined override gate (`OverrideEnabled`) and
// a known (non-custom) effort.
func EffortForConfigurationUpdate(overrideEnabled bool, info *model.ModelInfo, effectiveEffort string) (string, bool) {
	if !overrideEnabled || info == nil {
		return "", false
	}
	effective := strings.TrimSpace(effectiveEffort)
	if effective == "" {
		effective = strings.TrimSpace(info.DefaultReasoningLevel)
	}
	if effective == "" {
		return "", false
	}
	resolved := model.ResolveReasoningEffort(info, effective)
	if resolved == "" || !model.IsKnownReasoningEffort(resolved) {
		return "", false
	}
	return resolved, true
}

// LatestTrustedUpdate returns the most recent trusted configuration_update
// effort in model-visible history, whether it is the last item, and whether one
// exists. Untrusted updates never reach this input list (history conversion
// drops them).
func LatestTrustedUpdate(historyItems []any) (string, bool, bool) {
	for i := len(historyItems) - 1; i >= 0; i-- {
		payload, ok := configurationUpdatePayload(historyItems[i])
		if !ok || strings.TrimSpace(stringValue(payload, "type")) != "configuration_update" {
			continue
		}
		reasoning, _ := payload["reasoning"].(map[string]any)
		effort := strings.TrimSpace(stringValue(reasoning, "effort"))
		if effort == "" {
			continue
		}
		return effort, i == len(historyItems)-1, true
	}
	return "", false, false
}

// OverrideInputItems mirrors Rust `Session::record_reasoning_effort_override`:
// it decides whether this turn appends a trusted configuration_update for the
// selected effort, returning the items and the updated pin. It must be
// evaluated before the request effort is pinned for the turn.
func OverrideInputItems(pin Pin, modelSlug, effort string, overrideAvailable bool, historyItems []any) ([]any, Pin) {
	if !overrideAvailable || effort == "" {
		return nil, pin
	}
	if pin.Kind == PinCompacted {
		// Only a successful compaction retires the request baseline; the next
		// update re-activates the pin for the selected effort.
		pin.Pin(modelSlug, effort)
	}
	established, establishedIsTail, hasEstablished := LatestTrustedUpdate(historyItems)
	// Recovery adds no user message: reuse a matching trusted tail update even
	// before this runtime establishes its pin (Rust #44276).
	if hasEstablished && establishedIsTail && established == effort {
		return nil, pin
	}
	if pinned, ok := pin.Get(modelSlug); ok {
		compare := pinned
		if hasEstablished {
			compare = established
		}
		if compare == effort {
			return nil, pin
		}
	}
	return []any{ConfigurationUpdateInputItem(effort)}, pin
}

// RequestEffort mirrors Rust `Session::reasoning_effort_for_request`. Sampling
// pins the selected effort for the current model; compaction reuses the pin
// when it matches and never mutates it. `overrideEnabled` is the combined
// client-level gate (`OverrideEnabled`), so sampling with an unsupported model
// clears a stale baseline (#46530) just like a disabled feature does.
func RequestEffort(pin Pin, modelSlug, selectedEffort string, overrideEnabled bool, overrideEffort string, overrideAvailable bool, usage Usage) (string, Pin) {
	if !overrideEnabled {
		if usage == UsageSampling {
			pin = Pin{}
		}
		return selectedEffort, pin
	}
	if usage == UsageCompaction {
		if pinned, ok := pin.Get(modelSlug); ok {
			return pinned, pin
		}
	}
	if !overrideAvailable {
		if usage == UsageSampling {
			pin = Pin{}
		}
		return selectedEffort, pin
	}
	if usage == UsageSampling {
		effort := pin.Pin(modelSlug, overrideEffort)
		return effort, pin
	}
	return overrideEffort, pin
}

// ConfigurationUpdateInputItem builds the trusted configuration_update input
// item shape shared by the app-server and exec entry points.
func ConfigurationUpdateInputItem(effort string) map[string]any {
	return map[string]any{
		"type":      "configuration_update",
		"reasoning": map[string]any{"effort": effort},
	}
}

// ModelSlug returns the model's slug, falling back to modelID.
func ModelSlug(info *model.ModelInfo, fallback string) string {
	if info != nil && strings.TrimSpace(info.Slug) != "" {
		return strings.TrimSpace(info.Slug)
	}
	return strings.TrimSpace(fallback)
}

func configurationUpdatePayload(input any) (map[string]any, bool) {
	switch value := input.(type) {
	case map[string]any:
		return value, true
	case json.RawMessage:
		return decodeMap(value)
	case []byte:
		return decodeMap(value)
	case string:
		return decodeMap([]byte(value))
	default:
		return nil, false
	}
}

func decodeMap(data []byte) (map[string]any, bool) {
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false
	}
	return out, true
}

func stringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	text, _ := values[key].(string)
	return text
}
