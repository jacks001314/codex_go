package turn

import (
	"context"
	"encoding/json"
	"testing"

	"codex_go/skillprovider"
	"codex_go/tool"
)

func TestSkillsInstallationsListsAndFiltersDiscoveryV2Sources(t *testing.T) {
	entries := []skillprovider.CatalogEntry{
		{
			PackageID:     "skill://discovery:remote-a/home/user/.codex/skills/review",
			Authority:     skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: "discovery:remote-a"},
			Name:          "review",
			MainResource:  "skill://discovery:remote-a/home/user/.codex/skills/review/SKILL.md",
			Enabled:       true,
			EnvironmentID: "remote-a",
			SkillRoot:     "/home/user/.codex/skills/review",
		},
		{
			PackageID:     "skill://discovery:remote-b/workspace/skills/review",
			Authority:     skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: "discovery:remote-b"},
			Name:          "review",
			MainResource:  "skill://discovery:remote-b/workspace/skills/review/SKILL.md",
			Enabled:       true,
			EnvironmentID: "remote-b",
			SkillRoot:     "/workspace/skills/review",
		},
	}
	provider := skillprovider.ProviderFuncs{
		ListFunc: func(context.Context, skillprovider.ListQuery) (skillprovider.Catalog, error) {
			return skillprovider.Catalog{Entries: entries}, nil
		},
	}
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.EnableToolSearch = false
	options.DiscoveryV2Skills = true
	options.SkillProviders = skillprovider.NewRegistry(skillprovider.Source{Kind: skillprovider.SourceExecutor, Label: "executor", Provider: provider})
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatal(err)
	}
	router := tool.NewRouter(registry)
	output, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "installations",
		ToolName: tool.NamespacedName("skills", "installations"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"package":"skill://discovery:remote-a/home/user/.codex/skills/review"}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Installations []struct {
			EnvironmentID string `json:"environment_id"`
			SkillRoot     string `json:"skill_root"`
		} `json:"installations"`
	}
	if err := json.Unmarshal([]byte(output.Body), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Installations) != 2 {
		t.Fatalf("installations = %#v", response.Installations)
	}
	filtered, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "installations-filtered",
		ToolName: tool.NamespacedName("skills", "installations"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"package":"skill://discovery:remote-a/home/user/.codex/skills/review","source":{"kind":"executor","environment_id":"remote-b"}}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(filtered.Body), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Installations) != 1 || response.Installations[0].EnvironmentID != "remote-b" || response.Installations[0].SkillRoot != "/workspace/skills/review" {
		t.Fatalf("filtered = %#v", response.Installations)
	}
}
