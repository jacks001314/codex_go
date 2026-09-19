package model

import (
	"strings"
	"testing"
)

// Mirrors Rust's spawn-agent model resolution: the requested model must be
// advertised for the active multi-agent backend, and the error lists the
// picker-visible candidates, capped at five.
func TestSpawnAgentModelNameMatchesRust(t *testing.T) {
	available := []ModelPreset{
		{Model: "v2-visible", Visibility: VisibilityList, MultiAgentVersion: "v2", DefaultReasoningLevel: "medium", SupportedReasoningLevels: []string{"low", "medium", "high"}},
		{Model: "v1-visible", Visibility: VisibilityList, MultiAgentVersion: "v1"},
		{Model: "unversioned", Visibility: VisibilityList},
		{Model: "v2-hidden", Visibility: VisibilityHide, MultiAgentVersion: "v2"},
		{Model: "v2-disabled", Visibility: VisibilityList, MultiAgentVersion: "disabled"},
	}

	// The V2 backend accepts modules that are not explicitly disabled, including
	// presets with no version or a v1 version (Rust model_supports_multi_agent_backend).
	for _, want := range []string{"v2-visible", "v1-visible", "unversioned"} {
		got, err := SpawnAgentModelName(available, want, "v2")
		if err != nil || got != want {
			t.Fatalf("SpawnAgentModelName(%q, v2) = %q/%v, want %q", want, got, err, want)
		}
	}
	// An explicitly disabled preset is rejected even though it is visible.
	_, err := SpawnAgentModelName(available, "v2-disabled", "v2")
	if err == nil || !strings.Contains(err.Error(), "Unknown model `v2-disabled`") {
		t.Fatalf("disabled preset error = %v", err)
	}
	// The available list only names picker-visible candidates for this backend.
	if !strings.Contains(err.Error(), "Available models: v2-visible, v1-visible, unversioned") {
		t.Fatalf("available models = %v", err)
	}
	// Other backends accept every preset.
	if _, err := SpawnAgentModelName(available, "v2-disabled", "v1"); err != nil {
		t.Fatalf("v1 lookup of a disabled preset = %v", err)
	}
	if ModelPresetSupportsMultiAgentBackend(ModelPreset{MultiAgentVersion: "disabled"}, "v2") {
		t.Fatal("a disabled preset must not support the V2 backend")
	}
}

func TestSpawnAgentModelNameCapsAvailableModels(t *testing.T) {
	available := make([]ModelPreset, 0, 8)
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		available = append(available, ModelPreset{Model: name, Visibility: VisibilityList, MultiAgentVersion: "v2"})
	}
	_, err := SpawnAgentModelName(available, "missing", "v2")
	if err == nil {
		t.Fatal("an unknown model must fail")
	}
	_, available2, found := strings.Cut(err.Error(), "Available models: ")
	if !found || available2 != "a, b, c, d, e" {
		t.Fatalf("available models were not capped at five: %v", err)
	}
}

// Mirrors Rust's validate_spawn_agent_reasoning_effort message.
func TestValidateSpawnAgentReasoningEffortMatchesRust(t *testing.T) {
	supported := []string{"low", "medium", "high"}
	if err := ValidateSpawnAgentReasoningEffort("gpt-5.4", supported, "high"); err != nil {
		t.Fatalf("supported effort rejected: %v", err)
	}
	err := ValidateSpawnAgentReasoningEffort("gpt-5.4", supported, "ultra")
	if err == nil {
		t.Fatal("an unsupported effort must fail")
	}
	want := "Reasoning effort `ultra` is not supported for model `gpt-5.4`. Supported reasoning efforts: low, medium, high"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}
