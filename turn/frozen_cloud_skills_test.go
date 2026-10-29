package turn

import (
	"context"
	"encoding/json"
	"testing"

	"codex_go/skillprovider"
	"codex_go/tool"
)

const frozenCloudSkillsSnapshot = `{"skills":[` +
	`{"package":"skill://demo/work","name":"demo:work","description":"Do the work.",` +
	`"alias_root":"skill://demo","main_resource":"skill://demo/work/SKILL.md",` +
	`"resources":{"skill://demo/work/SKILL.md":"# Work\nExact frozen contents.\n"}},` +
	`{"package":"skill://demo/hidden","name":"demo:hidden","description":"Hidden.",` +
	`"alias_root":"skill://demo","prompt_visible":false,` +
	`"main_resource":"skill://demo/hidden/SKILL.md",` +
	`"resources":{"skill://demo/hidden/SKILL.md":"hidden contents"}}]}`

func frozenCloudSkillsRouter(t *testing.T) *tool.Router {
	t.Helper()
	provider, err := skillprovider.FrozenCloudProviderFromJSON([]byte(frozenCloudSkillsSnapshot))
	if err != nil {
		t.Fatalf("FrozenCloudProviderFromJSON() error = %v", err)
	}
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.EnableToolSearch = false
	options.CloudSkillProvider = provider
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	return tool.NewRouter(registry)
}

// TestFrozenCloudSkillsListAndReadUseTheNativeCatalogLikeRust covers Rust
// #55631: the cloud catalog and read handler work without an MCP server, keep
// aliases, and keep hidden skills readable.
func TestFrozenCloudSkillsListAndReadUseTheNativeCatalogLikeRust(t *testing.T) {
	router := frozenCloudSkillsRouter(t)
	output, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "list",
		ToolName: tool.NamespacedName("skills", "list"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"authority":{"kind":"orchestrator"}}`},
	})
	if err != nil {
		t.Fatalf("skills.list error = %v", err)
	}
	var listed skillsListResponse
	if err := json.Unmarshal([]byte(output.Body), &listed); err != nil {
		t.Fatalf("skills.list body = %q: %v", output.Body, err)
	}
	if len(listed.Skills) != 2 {
		t.Fatalf("skills.list returned %d skills, want 2: %s", len(listed.Skills), output.Body)
	}
	if listed.Skills[0].Package != "skill://demo/hidden" || listed.Skills[1].Package != "skill://demo/work" {
		t.Fatalf("skills.list order = %#v", listed.Skills)
	}
	for _, skill := range listed.Skills {
		if skill.Authority.Kind != "orchestrator" {
			t.Fatalf("authority = %#v, want orchestrator", skill.Authority)
		}
	}

	read, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "read",
		ToolName: tool.NamespacedName("skills", "read"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"package":"c0/work"}`},
	})
	if err != nil {
		t.Fatalf("skills.read error = %v", err)
	}
	var result skillsReadResponse
	if err := json.Unmarshal([]byte(read.Body), &result); err != nil {
		t.Fatalf("skills.read body = %q: %v", read.Body, err)
	}
	if result.Resource != "skill://demo/work/SKILL.md" || result.Contents != "# Work\nExact frozen contents.\n" || result.NextCursor != nil {
		t.Fatalf("skills.read = %#v", result)
	}

	hidden, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "read-hidden",
		ToolName: tool.NamespacedName("skills", "read"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"package":"c0/hidden"}`},
	})
	if err != nil {
		t.Fatalf("hidden skills.read error = %v", err)
	}
	if err := json.Unmarshal([]byte(hidden.Body), &result); err != nil {
		t.Fatalf("hidden skills.read body = %q: %v", hidden.Body, err)
	}
	if result.Contents != "hidden contents" {
		t.Fatalf("hidden contents = %q", result.Contents)
	}
}
