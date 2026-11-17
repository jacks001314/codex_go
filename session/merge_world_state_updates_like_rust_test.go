package session

import "testing"

// mergeWorldStateTestMessage builds the response item Rust's
// `build_rendered_message` produces for one merged role group.
func mergeWorldStateTestMessage(role string, kind string, texts ...string) map[string]any {
	content := make([]any, 0, len(texts))
	kinds := make([]any, 0, len(texts))
	for _, text := range texts {
		content = append(content, map[string]any{"type": "input_text", "text": text})
		kinds = append(kinds, kind)
	}
	item := map[string]any{"type": "message", "role": role, "content": content}
	if kind != "" {
		item["internal_chat_message_metadata_passthrough"] = map[string]any{
			"content_item_kinds": kinds,
		}
	}
	return item
}

// Mirrors Rust `updates_tests::updates_preserve_item_boundaries_roles_and_standalone_fragments`
// (core/src/context_manager/updates.rs): explicit response items split pending
// fragments, standalone fragments never merge, and a role change splits the
// pending group.
func TestMergeWorldStateUpdatesPreservesItemBoundariesRolesLikeRust(t *testing.T) {
	const kind = "test.context"
	tools := liteAdditionalTools([]any{liteDeclaration("lookup")})
	mergeable := func(role string, text string) WorldStateUpdate {
		return WorldStateUpdate{Placement: WorldStateMergeable, Role: role, Text: text, Kind: kind}
	}
	updates := []WorldStateUpdate{
		mergeable("developer", "before one"),
		mergeable("developer", "before two"),
		{Placement: WorldStatePrefix, Role: "developer", Text: "prefix", Kind: kind},
		mergeable("developer", "after prefix"),
		{Placement: WorldStatePrefix, Item: liteAdditionalTools([]any{liteDeclaration("lookup")})},
		mergeable("developer", "after"),
		{Placement: WorldStateStandalone, Role: "developer", Text: "standalone", Kind: kind},
		mergeable("developer", "last developer"),
		mergeable("user", "user"),
		{Placement: WorldStateStandalone, Item: liteAdditionalTools([]any{liteDeclaration("lookup")})},
	}
	items := MergeWorldStateUpdates(updates)
	want := []any{
		mergeWorldStateTestMessage("developer", kind, "before one", "before two"),
		mergeWorldStateTestMessage("developer", kind, "prefix"),
		mergeWorldStateTestMessage("developer", kind, "after prefix"),
		tools,
		mergeWorldStateTestMessage("developer", kind, "after"),
		mergeWorldStateTestMessage("developer", kind, "standalone"),
		mergeWorldStateTestMessage("developer", kind, "last developer"),
		mergeWorldStateTestMessage("user", kind, "user"),
		tools,
	}
	if len(items) != len(want) {
		t.Fatalf("merged items = %#v, want %d items", items, len(want))
	}
	for index := range want {
		if !sameWorldStateJSON(t, want[index], items[index]) {
			t.Fatalf("item %d = %#v, want %#v", index, items[index], want[index])
		}
	}
}
