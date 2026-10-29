package appserver

import (
	"strings"
	"testing"

	promptctx "codex_go/prompt"
	"codex_go/skillprovider"
)

const frozenCloudAppServerSnapshot = `{"skills":[` +
	`{"package":"skill://demo/work","name":"demo:work","description":"Do the work.",` +
	`"alias_root":"skill://demo","main_resource":"skill://demo/work/SKILL.md",` +
	`"resources":{"skill://demo/work/SKILL.md":"# Work\nExact frozen contents.\n"}},` +
	`{"package":"skill://demo/hidden","name":"demo:hidden","description":"Hidden.",` +
	`"alias_root":"skill://demo","prompt_visible":false,` +
	`"main_resource":"skill://demo/hidden/SKILL.md",` +
	`"resources":{"skill://demo/hidden/SKILL.md":"hidden contents"}}]}`

// TestFrozenCloudSkillsReplaceMCPDiscoveryLikeRust covers Rust #55631: the
// frozen snapshot supplies the cloud catalog and reads even though the router
// has no MCP service, and prompt_visible=false hides only the model catalog.
func TestFrozenCloudSkillsReplaceMCPDiscoveryLikeRust(t *testing.T) {
	provider, err := skillprovider.FrozenCloudProviderFromJSON([]byte(frozenCloudAppServerSnapshot))
	if err != nil {
		t.Fatalf("FrozenCloudProviderFromJSON() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{CloudSkills: provider})

	metadata, warnings := router.orchestratorSkillMetadataForRuntime("thread-frozen-cloud", "turn-1")
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if len(metadata) != 1 {
		t.Fatalf("metadata = %#v, want only the prompt-visible skill", metadata)
	}
	skill := metadata[0]
	if skill.Name != "demo:work" || skill.PackageID != "skill://demo/work" || skill.ResourceID != "skill://demo/work/SKILL.md" {
		t.Fatalf("skill metadata = %#v", skill)
	}
	if skill.Scope != "orchestrator" || skill.AuthorityKind != "orchestrator" {
		t.Fatalf("skill authority = %#v", skill)
	}

	contents, err := router.readOrchestratorSkillResourceForRuntime("thread-frozen-cloud", promptctx.InstructionsSkillMetadata{
		PackageID:  "skill://demo/work",
		ResourceID: "skill://demo/work/SKILL.md",
	})
	if err != nil || contents != "# Work\nExact frozen contents.\n" {
		t.Fatalf("read = %q err=%v", contents, err)
	}

	if _, err := router.readOrchestratorSkillResourceForRuntime("thread-frozen-cloud", promptctx.InstructionsSkillMetadata{
		PackageID:  "skill://demo/work",
		ResourceID: "skill://demo/work/other.md",
	}); err == nil || !strings.Contains(err.Error(), "unknown frozen skill resource") {
		t.Fatalf("unknown resource error = %v", err)
	}
}
