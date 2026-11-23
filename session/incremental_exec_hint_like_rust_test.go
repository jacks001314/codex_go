package session

import (
	"encoding/json"
	"strings"
	"testing"
)

// Mirrors Rust #63046: an incremental update that only redeclares the Code Mode
// exec tool still carries the incremental tools hint, because the exec
// declaration embeds the global helper text.
func TestIncrementalToolsHintAccompaniesExecOnlyRefreshLikeRust(t *testing.T) {
	execTool := func(helper string) map[string]any {
		return map[string]any{
			"type": "custom", "name": "exec",
			"description": "Run JavaScript.\n\nGlobal helpers:\n" + helper,
		}
	}
	previous, err := TopLevelToolsHashes([]any{execTool("image(value)")})
	if err != nil {
		t.Fatalf("TopLevelToolsHashes() error = %v", err)
	}
	messages := ResolveIncrementalToolMessages(nil)
	_, updates, err := TopLevelToolsUpdatesWithMessages([]any{execTool("image(value, detail)")}, previous, true, messages)
	if err != nil {
		t.Fatalf("TopLevelToolsUpdatesWithMessages() error = %v", err)
	}
	merged := mergeTopLevelTestUpdates(MergeWorldStateUpdates(updates))
	if len(merged) != 2 {
		t.Fatalf("merged updates = %#v, want the hint and the redeclared exec tool", merged)
	}
	if merged[0]["type"] != "message" || !strings.Contains(marshalTopLevelTestItem(t, merged[0]), messages.ToolUpdateHint) {
		t.Fatalf("first update = %#v, want the incremental tools hint", merged[0])
	}
	added, _ := merged[1]["tools"].([]any)
	if len(added) != 1 || topLevelDefinitionName(added[0]) != "exec" {
		t.Fatalf("redeclared tools = %#v, want only exec", added)
	}

	// A plain tool refresh keeps the pre-#63046 behavior: no hint.
	lookup := func(text string) map[string]any {
		return map[string]any{"type": "function", "name": "lookup", "description": text}
	}
	previous, err = TopLevelToolsHashes([]any{lookup("v1")})
	if err != nil {
		t.Fatalf("TopLevelToolsHashes() error = %v", err)
	}
	_, updates, err = TopLevelToolsUpdatesWithMessages([]any{lookup("v2")}, previous, true, messages)
	if err != nil {
		t.Fatalf("TopLevelToolsUpdatesWithMessages() error = %v", err)
	}
	merged = mergeTopLevelTestUpdates(MergeWorldStateUpdates(updates))
	if len(merged) != 1 {
		t.Fatalf("plain tool update = %#v, want a single additional_tools item", merged)
	}
	if _, hinted := merged[0]["content"]; hinted {
		t.Fatalf("plain tool update carried the incremental hint: %#v", merged[0])
	}
}

func marshalTopLevelTestItem(t *testing.T, item any) string {
	t.Helper()
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal item: %v", err)
	}
	return string(data)
}

func mergeTopLevelTestUpdates(items []any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			out = append(out, object)
		}
	}
	return out
}
