package historycell

import (
	"reflect"
	"testing"
)

// Mirrors Rust's `warning_count_deduplicates_messages_mcp_summaries_and_
// composites`: identities deduplicate replayed cells and repeated diagnostics,
// and entries sharing an identity merge their details.
func TestWarningEntriesAndCountLikeRust(t *testing.T) {
	cells := []WarningCell{
		NewStartupWarnings([]string{"Repeated warning"}),
		NewMCPStartupWarnings([]string{"MCP alpha failed"}, []string{"alpha"}, ""),
		NewMCPStartupWarnings([]string{"MCP startup incomplete"}, []string{"alpha"}, ""),
	}
	if got := WarningCount(cells); got != 2 {
		t.Fatalf("WarningCount() = %d, want 2", got)
	}
	want := []WarningEntry{
		{ID: WarningId{Kind: WarningIdMessage, Value: "Repeated warning"}, Source: "Startup", Details: "Repeated warning"},
		{ID: WarningId{Kind: WarningIdMCPServer, Value: "alpha"}, Source: "MCP \u00b7 alpha", Details: "MCP alpha failed\n\nMCP startup incomplete"},
	}
	if got := WarningEntries(cells); !reflect.DeepEqual(got, want) {
		t.Fatalf("WarningEntries() = %#v, want %#v", got, want)
	}
	// Replayed cells do not increase the count or change the merged entries.
	replay := append(append([]WarningCell{}, cells...), cells...)
	if got := WarningCount(replay); got != 2 {
		t.Fatalf("replayed WarningCount() = %d, want 2", got)
	}
	if got := WarningEntries(replay); !reflect.DeepEqual(got, want) {
		t.Fatalf("replayed WarningEntries() = %#v, want %#v", got, want)
	}
	if got := WarningCount(nil); got != 0 {
		t.Fatalf("WarningCount(nil) = %d, want 0", got)
	}
}

// Mirrors Rust's sign-in note and the per-server details fallback.
func TestWarningEntriesMCPSignInAndFallbackLikeRust(t *testing.T) {
	cell := NewMCPStartupWarnings([]string{"MCP alpha failed"}, []string{"alpha", "beta"}, MCPStartupFailureReauthenticationRequired)
	// Beta has no recorded detail of its own; the developer removed it to pin the
	// fallback path Rust uses when `mcp_details` has no entry.
	delete(cell.MCPDetails, "beta")
	entries := cell.WarningEntries()
	want := []WarningEntry{
		{ID: WarningId{Kind: WarningIdMCPServer, Value: "alpha"}, Source: "MCP \u00b7 alpha", Details: "MCP alpha failed\n\nSign-in required."},
		{ID: WarningId{Kind: WarningIdMCPServer, Value: "beta"}, Source: "MCP \u00b7 beta", Details: "MCP startup incomplete: beta\n\nSign-in required."},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("WarningEntries() = %#v, want %#v", entries, want)
	}
}

// Mirrors App::merge_startup_warnings: per-server details merge with distinct
// diagnostics while the sign-in subset is retained.
func TestStartupWarningsMergeDetailsLikeRust(t *testing.T) {
	merged := NewMCPStartupWarnings([]string{"first"}, []string{"alpha"}, "").
		Merge(NewMCPStartupWarnings([]string{"second", "first"}, []string{"alpha"}, MCPStartupFailureReauthenticationRequired))
	if got := merged.MCPDetails["alpha"]; !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("merged MCPDetails = %#v, want [first second]", got)
	}
	if !merged.SignInServers["alpha"] {
		t.Fatal("merged sign-in subset lost alpha")
	}
	if got := merged.WarningEntries(); len(got) != 1 || got[0].Details != "first\n\nsecond\n\nSign-in required." {
		t.Fatalf("merged WarningEntries = %#v", got)
	}
}
