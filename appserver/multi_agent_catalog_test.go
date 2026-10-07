package appserver

import (
	"encoding/json"
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

// Rust #51329 keeps stripping inherited `multi_agent.role_instructions` and
// `multi_agent.usage_hint` content whose wording predates the current bundled
// instructions. Go's fork filter is marker-based rather than kind-based, so the
// persisted wording is irrelevant: any parent role/mode/usage-hint fragment is
// removed.
func TestFilterInheritedDeveloperFragmentsDropsStaleWordingLikeRust(t *testing.T) {
	items := []session.Item{
		{ID: "keep", Type: "message", Text: "ordinary developer message"},
		{ID: "stale-hint", Type: "message", Text: "<multi_agent_usage_hint>an ancient hint wording that no bundled string matches</multi_agent_usage_hint>"},
		{ID: "stale-role", Type: "message", Content: []session.ContentPart{{Type: "input_text", Text: "<multi_agent_role>an ancient role wording</multi_agent_role>"}}},
	}
	filtered := filterInheritedCurrentTimeReminders(items)
	if len(filtered) != 1 || filtered[0].ID != "keep" {
		t.Fatalf("filtered = %#v, want only the ordinary developer message", filtered)
	}
}

// Rust #51329 "unmarked bundled hints": a persisted developer hint can predate
// the current bundled wording and carry no marker tag at all. Rust drops it by
// the harness-owned `content_item_kinds` classification; Go reads the same
// positional metadata off the persisted item.
func TestFilterInheritedDeveloperFragmentsDropsUnmarkedHintByKindLikeRust(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": "Previous parent root guidance."},
		},
		"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []any{"multi_agent.usage_hint"},
		},
	})
	if err != nil {
		t.Fatalf("marshal hint item error = %v", err)
	}
	roleRaw, err := json.Marshal(map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": "an ancient role wording"},
		},
		"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []any{"multi_agent.role_instructions"},
		},
	})
	if err != nil {
		t.Fatalf("marshal role item error = %v", err)
	}
	items := []session.Item{
		{ID: "keep", Type: "message", Role: "developer", Text: "ordinary developer message"},
		{ID: "unmarked-hint", Type: "message", Role: "developer", Raw: raw, Text: "Previous parent root guidance."},
		{ID: "unmarked-role", Type: "message", Role: "developer", Raw: roleRaw, Text: "an ancient role wording"},
	}
	filtered := filterInheritedCurrentTimeReminders(items)
	if len(filtered) != 1 || filtered[0].ID != "keep" {
		t.Fatalf("filtered = %#v, want only the ordinary developer message", filtered)
	}
}

// Rust #51329 `set_annotated_content`: dropping a classified content item keeps
// the surviving items' classifications aligned with their positions.
func TestFilterInheritedDeveloperFragmentsRealignsContentItemKindsLikeRust(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": "Previous parent root guidance."},
			map[string]any{"type": "input_text", "text": "Preserved compacted developer context."},
		},
		"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []any{"multi_agent.usage_hint", "unknown"},
		},
	})
	if err != nil {
		t.Fatalf("marshal mixed item error = %v", err)
	}
	items := []session.Item{{
		ID:   "mixed",
		Type: "message",
		Role: "developer",
		Raw:  raw,
		Content: []session.ContentPart{
			{Type: "input_text", Text: "Previous parent root guidance."},
			{Type: "input_text", Text: "Preserved compacted developer context."},
		},
	}}
	filtered := filterInheritedCurrentTimeReminders(items)
	if len(filtered) != 1 || len(filtered[0].Content) != 1 ||
		filtered[0].Content[0].Text != "Preserved compacted developer context." {
		t.Fatalf("filtered = %#v, want only the preserved content item", filtered)
	}
	kinds := sessionItemContentItemKinds(&filtered[0])
	if len(kinds) != 1 || kinds[0] != "unknown" {
		t.Fatalf("realigned kinds = %#v, want [unknown]", kinds)
	}
}

// Rust #51329 scopes the kind-based scrub to developer messages; other roles are
// kept untouched even when they somehow carry a hint classification.
func TestFilterInheritedDeveloperFragmentsKeepsNonDeveloperKindContentLikeRust(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"type": "message",
		"role": "user",
		"content": []any{
			map[string]any{"type": "input_text", "text": "user text"},
		},
		"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []any{"multi_agent.usage_hint"},
		},
	})
	if err != nil {
		t.Fatalf("marshal user item error = %v", err)
	}
	items := []session.Item{{ID: "user", Type: "message", Role: "user", Raw: raw, Text: "user text"}}
	filtered := filterInheritedCurrentTimeReminders(items)
	if len(filtered) != 1 || filtered[0].ID != "user" {
		t.Fatalf("filtered = %#v, want the user message kept", filtered)
	}
}

func TestFilterInheritedDeveloperFragmentsPreservesUnrelatedContentLikeRust(t *testing.T) {
	items := []session.Item{
		{
			ID:   "compound",
			Type: "message",
			Content: []session.ContentPart{
				{Type: "input_text", Text: "<multi_agent_role>\nparent root role\n</multi_agent_role>"},
				{Type: "input_text", Text: "<multi_agent_mode>\nproactive\n</multi_agent_mode>"},
				{Type: "input_text", Text: "unrelated context that must survive"},
			},
		},
	}
	filtered := filterInheritedCurrentTimeReminders(items)
	if len(filtered) != 1 || filtered[0].ID != "compound" || len(filtered[0].Content) != 1 ||
		filtered[0].Content[0].Text != "unrelated context that must survive" {
		t.Fatalf("filtered = %#v, want only the unrelated content item", filtered)
	}
}
