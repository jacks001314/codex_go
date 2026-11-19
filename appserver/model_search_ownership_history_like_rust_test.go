package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/mcp"
	"codex_go/retainedctx"
	"codex_go/session"
)

func modelSearchHarnessItem(t *testing.T, fields map[string]any) session.Item {
	t.Helper()
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return session.Item{ID: "item", Type: "message", Data: map[string]any{harnessMetadataKey: json.RawMessage(encoded)}}
}

// TestModelSearchStateRestorePrecedenceLikeRust mirrors Rust #62144: saved
// state wins, legacy attribution and native markers keep older rollouts from
// switching providers, and imported history without checkpoints withholds
// native fallback.
func TestModelSearchStateRestorePrecedenceLikeRust(t *testing.T) {
	cases := []struct {
		name  string
		items []session.Item
		fresh bool
		want  modelSearchProvider
	}{
		{
			name:  "saved search mcp",
			items: []session.Item{modelSearchHarnessItem(t, map[string]any{"model_search_state": string(retainedctx.ModelSearchStateSearchMCP)})},
			want:  modelSearchClientMCP,
		},
		{
			name:  "saved standalone",
			items: []session.Item{modelSearchHarnessItem(t, map[string]any{"model_search_state": string(retainedctx.ModelSearchStateStandalone)})},
			want:  modelSearchNative,
		},
		{
			name: "search mcp wins over an older standalone checkpoint",
			items: []session.Item{
				modelSearchHarnessItem(t, map[string]any{"model_search_state": string(retainedctx.ModelSearchStateStandalone)}),
				modelSearchHarnessItem(t, map[string]any{"model_search_state": string(retainedctx.ModelSearchStateSearchMCP)}),
			},
			want: modelSearchClientMCP,
		},
		{
			name:  "legacy attribution error claims search mcp",
			items: []session.Item{modelSearchHarnessItem(t, map[string]any{"mcp_attribution": map[string]any{"status": string(retainedctx.McpAttributionStatusAttributionError)}})},
			want:  modelSearchClientMCP,
		},
		{
			name:  "legacy search connector claims search mcp",
			items: []session.Item{modelSearchHarnessItem(t, map[string]any{"mcp_attribution": map[string]any{"status": string(retainedctx.McpAttributionStatusComplete), "sources": []any{map[string]any{"connector_id": mcp.SearchConnector, "server_name": "s", "tool_name": "t", "first_turn_id": "turn"}}}})},
			want:  modelSearchClientMCP,
		},
		{
			name:  "legacy native marker restores standalone",
			items: []session.Item{modelSearchHarnessItem(t, map[string]any{"native_search_fallback_used": true})},
			want:  modelSearchNative,
		},
		{
			name:  "unused checkpoint stays unused",
			items: []session.Item{modelSearchHarnessItem(t, map[string]any{"model_search_state": string(retainedctx.ModelSearchStateUnused)})},
			want:  "",
		},
		{
			name:  "imported history without checkpoints withholds native",
			items: []session.Item{{ID: "plain", Type: "message"}},
			want:  modelSearchClientMCP,
		},
		{
			name:  "fresh history without checkpoints stays unowned",
			items: []session.Item{{ID: "plain", Type: "message"}},
			fresh: true,
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scanModelSearchHistory(tc.items).restoredProvider(tc.fresh); got != tc.want {
				t.Fatalf("restored provider = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAnnotateModelSearchMetadataCheckpointsEveryOwnerLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	if !router.claimModelSearchProvider("thread-mcp", modelSearchClientMCP) {
		t.Fatal("failed to claim Search MCP")
	}
	mcpItems := []session.Item{{ID: "search", Type: "web_search_call"}}
	router.annotateModelSearchMetadata("thread-mcp", mcpItems)
	var fields map[string]any
	if err := json.Unmarshal(harnessMetadataRawFromItem(&mcpItems[0]), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["model_search_state"] != string(retainedctx.ModelSearchStateSearchMCP) {
		t.Fatalf("mcp checkpoint = %#v", fields)
	}
	if _, ok := fields["native_search_fallback_used"]; ok {
		t.Fatalf("mcp checkpoint carried the native marker: %#v", fields)
	}

	if !router.claimModelSearchProvider("thread-native", modelSearchNative) {
		t.Fatal("failed to claim native search")
	}
	nativeItems := []session.Item{{ID: "search", Type: "web_search_call"}}
	router.annotateModelSearchMetadata("thread-native", nativeItems)
	fields = nil
	if err := json.Unmarshal(harnessMetadataRawFromItem(&nativeItems[0]), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["model_search_state"] != string(retainedctx.ModelSearchStateStandalone) || fields["native_search_fallback_used"] != true {
		t.Fatalf("native checkpoint = %#v", fields)
	}
}
