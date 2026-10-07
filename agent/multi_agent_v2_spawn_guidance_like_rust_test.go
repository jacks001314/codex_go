package agent

import (
	"strings"
	"testing"
)

// TestMultiAgentV2SpawnDescriptionIncludesInheritedModelGuidanceLikeRust
// mirrors Rust #26114 (codex-rs/core/src/tools/handlers/multi_agents_spec.rs):
// the V2 `spawn_agent` description carries
// SPAWN_AGENT_INHERITED_MODEL_GUIDANCE whenever the model overrides are exposed
// and the spawn metadata is not hidden, and the guidance survives a catalog
// description override (Rust #46123 keeps runtime guidance while replacing the
// bundled text). The `model_catalog_in_context` variant from #49786 has no Go
// carrier (the feature is unwired) and is deliberately out of scope here.
func TestMultiAgentV2SpawnDescriptionIncludesInheritedModelGuidanceLikeRust(t *testing.T) {
	catalogDescription := "Catalog supplied spawn description."
	hint := "Configured usage hint."

	for _, tc := range []struct {
		name            string
		hideSpawn       bool
		exposeModel     bool
		catalogOverride bool
		usageHint       bool
		wantGuidance    bool
	}{
		{name: "metadata visible and overrides exposed", exposeModel: true, wantGuidance: true},
		{name: "catalog description keeps the guidance", exposeModel: true, catalogOverride: true, wantGuidance: true},
		{name: "usage hint stays after the guidance", exposeModel: true, usageHint: true, wantGuidance: true},
		{name: "metadata hidden", hideSpawn: true, exposeModel: true},
		{name: "overrides not exposed", hideSpawn: false, exposeModel: false},
		{name: "hidden metadata without overrides", hideSpawn: true, exposeModel: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec := &multiAgentV2ToolExecutor{
				kind:                      multiAgentV2Spawn,
				hideSpawnMetadata:         tc.hideSpawn,
				exposeSpawnModelOverrides: tc.exposeModel,
			}
			if tc.catalogOverride {
				exec.toolOverrides = map[string]MultiAgentToolOverride{
					string(multiAgentV2Spawn): {Description: &catalogDescription},
				}
			}
			if tc.usageHint {
				exec.usageHintText = &hint
			}
			description := exec.Spec().Description
			if got := strings.Contains(description, multiAgentV2SpawnInheritedModelGuidance); got != tc.wantGuidance {
				t.Fatalf("inherited-model guidance present = %v, want %v; description = %q", got, tc.wantGuidance, description)
			}
			if tc.catalogOverride && !strings.Contains(description, catalogDescription) {
				t.Fatalf("catalog description was dropped: %q", description)
			}
			if !tc.wantGuidance {
				return
			}
			guidanceAt := strings.Index(description, multiAgentV2SpawnInheritedModelGuidance)
			if tc.usageHint {
				// Rust composes the usage hint onto the finished description, so
				// the guidance must precede it.
				hintAt := strings.Index(description, hint)
				if hintAt < 0 || hintAt < guidanceAt {
					t.Fatalf("usage hint ordering = %d (guidance at %d); description = %q", hintAt, guidanceAt, description)
				}
			}
		})
	}
}
