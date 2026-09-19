package model

import (
	"fmt"
	"strings"
)

// MaxSpawnAgentModelOverrides caps how many models an unknown-model error lists
// (Rust `MAX_SPAWN_AGENT_MODEL_OVERRIDES`).
const MaxSpawnAgentModelOverrides = 5

// ModelPresetShowInPicker mirrors Rust `ModelPreset::show_in_picker`, which is
// `visibility == ModelVisibility::List`. The Go TUI picker additionally accepts
// the Go-only "visible" value, so this stays a separate predicate.
func ModelPresetShowInPicker(preset ModelPreset) bool {
	return strings.EqualFold(strings.TrimSpace(preset.Visibility), VisibilityList)
}

// ModelPresetSupportsMultiAgentBackend mirrors Rust's
// `model_supports_multi_agent_backend`: the V2 backend accepts every preset that
// is not explicitly disabled, and the other backends accept every preset.
func ModelPresetSupportsMultiAgentBackend(preset ModelPreset, multiAgentVersion string) bool {
	if strings.TrimSpace(multiAgentVersion) != "v2" {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(preset.MultiAgentVersion), "disabled")
}

// SpawnAgentModelName resolves a requested spawn-agent model against the catalog
// presets (Rust `find_spawn_agent_model_name`): the model must be advertised for
// the active multi-agent backend, and the error lists the picker-visible
// candidates, capped at MaxSpawnAgentModelOverrides.
func SpawnAgentModelName(available []ModelPreset, requested string, multiAgentVersion string) (string, error) {
	requested = strings.TrimSpace(requested)
	for _, candidate := range available {
		if strings.TrimSpace(candidate.Model) == requested &&
			ModelPresetSupportsMultiAgentBackend(candidate, multiAgentVersion) {
			return candidate.Model, nil
		}
	}
	names := make([]string, 0, MaxSpawnAgentModelOverrides)
	for _, candidate := range available {
		if !ModelPresetShowInPicker(candidate) || !ModelPresetSupportsMultiAgentBackend(candidate, multiAgentVersion) {
			continue
		}
		names = append(names, candidate.Model)
		if len(names) == MaxSpawnAgentModelOverrides {
			break
		}
	}
	return "", fmt.Errorf("Unknown model `%s` for spawn_agent. Available models: %s", requested, strings.Join(names, ", "))
}

// ValidateSpawnAgentReasoningEffort mirrors Rust's
// `validate_spawn_agent_reasoning_effort`.
func ValidateSpawnAgentReasoningEffort(model string, supported []string, requested string) error {
	requested = strings.TrimSpace(requested)
	for _, level := range supported {
		if strings.TrimSpace(level) == requested {
			return nil
		}
	}
	return fmt.Errorf(
		"Reasoning effort `%s` is not supported for model `%s`. Supported reasoning efforts: %s",
		requested,
		model,
		strings.Join(supported, ", "),
	)
}
