package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

func TestMultiAgentCatalogRoleInstructionsResolveFromModelMessagesLikeRust(t *testing.T) {
	catalogPath := filepath.Join(t.TempDir(), "models.json")
	catalog := `{"models":[{
		"slug": "gpt-test",
		"display_name": "GPT Test",
		"model_messages": {
			"multi_agent": {
				"role": {"root": "catalog root role", "subagent": "catalog subagent role"}
			}
		}
	}]}`
	if err := os.WriteFile(catalogPath, []byte(catalog), 0o600); err != nil {
		t.Fatalf("WriteFile catalog error = %v", err)
	}
	cfg := &config.Config{Values: map[string]any{"model_catalog_json": catalogPath}}
	router := &RuntimeRouter{}

	root, subagent := router.multiAgentCatalogRoleInstructions(cfg, &turn.TurnStartParams{Model: "gpt-test"})
	if root == nil || *root != "catalog root role" {
		t.Fatalf("root = %#v", root)
	}
	if subagent == nil || *subagent != "catalog subagent role" {
		t.Fatalf("subagent = %#v", subagent)
	}

	// Unknown model yields no catalog instructions.
	root, subagent = router.multiAgentCatalogRoleInstructions(cfg, &turn.TurnStartParams{Model: "missing"})
	if root != nil || subagent != nil {
		t.Fatalf("unknown model catalog roles = %#v/%#v", root, subagent)
	}
}

func TestFilterInheritedDeveloperFragmentsDropsParentRoleGuidanceLikeRust(t *testing.T) {
	items := []session.Item{
		{ID: "keep", Type: "message", Text: "ordinary developer message"},
		{ID: "role", Type: "message", Text: "<multi_agent_role>\nparent root role\n</multi_agent_role>"},
		{ID: "reminder", Type: "message", Data: map[string]any{"kind": "current_time_reminder"}},
		{ID: "role-content", Type: "message", Content: []session.ContentPart{{Type: "input_text", Text: "<multi_agent_role>\nchild role\n</multi_agent_role>"}}},
	}
	filtered := filterInheritedCurrentTimeReminders(items)
	if len(filtered) != 1 || filtered[0].ID != "keep" {
		t.Fatalf("filtered = %#v, want only the ordinary developer message", filtered)
	}
}
