package turn

import (
	"testing"

	"codex_go/mcp"
)

func TestOrchestratorSkillMetadataCarriesPluginIDLikeRust(t *testing.T) {
	metadata, ok := orchestratorSkillMetadataFromResource(mcp.MCPResource{
		URI:         "skill://drive/doc-search",
		Name:        "docs",
		Description: "Search documentation.",
		MimeType:    orchestratorSkillMimeType,
		Meta: map[string]any{
			"plugin_name": "Drive",
			"plugin_id":   "drive@market",
			"skill_name":  "doc search",
		},
	})
	if !ok {
		t.Fatal("orchestratorSkillMetadataFromResource() = not ok")
	}
	if metadata.PluginID != "drive@market" {
		t.Fatalf("PluginID = %q, want drive@market", metadata.PluginID)
	}
}
