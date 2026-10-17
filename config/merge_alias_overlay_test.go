package config

import (
	"reflect"
	"testing"
)

// TestSparseOverlayPreservesUnvisitedBaseAliasesLikeRust mirrors Rust #47903:
// the overlay is normalized before merging, base aliases are normalized only in
// tables the overlay reaches, and unrelated base tables keep their raw alias
// keys so the returned config version matches the file on disk.
func TestSparseOverlayPreservesUnvisitedBaseAliasesLikeRust(t *testing.T) {
	base := map[string]any{
		"memories": map[string]any{"no_memories_if_mcp_or_web_search": false},
		"agents":   map[string]any{"max_threads": 2},
	}
	overlay := map[string]any{"agents": map[string]any{"max_depth": 3}}
	mergeConfigMaps(base, overlay)

	want := map[string]any{
		"memories": map[string]any{"no_memories_if_mcp_or_web_search": false},
		"agents": map[string]any{
			"max_concurrent_threads_per_session": 2,
			"max_depth":                          3,
		},
	}
	if !reflect.DeepEqual(base, want) {
		t.Fatalf("merged config = %#v, want %#v", base, want)
	}
	if !reflect.DeepEqual(overlay, map[string]any{"agents": map[string]any{"max_depth": 3}}) {
		t.Fatalf("merge mutated the overlay: %#v", overlay)
	}
}

// TestInsertedOverlaySubtreeAliasesNormalizedLikeRust pins the recursive
// normalization of a newly inserted table (Rust's normalize_key_aliases runs
// over the whole overlay).
func TestInsertedOverlaySubtreeAliasesNormalizedLikeRust(t *testing.T) {
	base := map[string]any{}
	overlay := map[string]any{"agents": map[string]any{"max_threads": 4}}
	mergeConfigMaps(base, overlay)
	agents, _ := base["agents"].(map[string]any)
	if _, raw := agents["max_threads"]; raw {
		t.Fatalf("inserted subtree kept the alias: %#v", agents)
	}
	if agents["max_concurrent_threads_per_session"] != 4 {
		t.Fatalf("inserted subtree = %#v", agents)
	}
}
