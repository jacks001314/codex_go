package rollout

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestApplyPersistedItemTruncationCapsCommandOutputLikeRust mirrors Rust
// rollout/src/policy.rs persisted_item_completed_event for paginated history
// (#50427): oversized aggregated command output is truncated in the middle,
// while small output and non-command items are returned unchanged.
func TestApplyPersistedItemTruncationCapsCommandOutputLikeRust(t *testing.T) {
	big := strings.Repeat("x", persistedCommandOutputMaxBytes+4096)
	raw, err := json.Marshal(map[string]any{
		"type":              "CommandExecution",
		"id":                "cmd-1",
		"aggregated_output": big,
		"exit_code":         0,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := applyPersistedItemTruncation(raw)
	var item map[string]any
	if err := json.Unmarshal(out, &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, _ := item["aggregated_output"].(string)
	if len(got) > persistedCommandOutputMaxBytes {
		t.Fatalf("aggregated_output len = %d, want <= %d", len(got), persistedCommandOutputMaxBytes)
	}
	if !strings.Contains(got, "command output truncated for persistence") {
		t.Fatalf("missing truncation marker: %q", got)
	}
	if strings.Contains(got, "chars truncated") {
		t.Fatalf("command output used the generic marker instead of the persistence marker: %q", got)
	}
	// Unrelated numeric fields survive re-encoding verbatim.
	if !strings.Contains(string(out), `"exit_code":0`) {
		t.Fatalf("exit_code not preserved: %s", out)
	}

	small, _ := json.Marshal(map[string]any{"type": "CommandExecution", "id": "cmd-2", "aggregated_output": "ok"})
	if outSmall := applyPersistedItemTruncation(small); string(outSmall) != string(small) {
		t.Fatalf("small command output changed: %s", outSmall)
	}

	other, _ := json.Marshal(map[string]any{"type": "AgentMessage", "id": "msg-1", "content": []any{}})
	if outOther := applyPersistedItemTruncation(other); string(outOther) != string(other) {
		t.Fatalf("non-command item changed: %s", outOther)
	}
}

// TestTruncateMCPResultValueLikeRust mirrors Rust truncate_mcp_tool_result
// (#50458/#50470) at a small budget: the oversized serialized result is
// replaced with a bounded text preview that preserves the error flag, drops
// structured content/meta, and is idempotent when re-truncated.
func TestTruncateMCPResultValueLikeRust(t *testing.T) {
	const maxBytes = 1024
	result := map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": "head\n" + strings.Repeat("\"\\\n", 5000) + "\ntail"}},
		"structuredContent": map[string]any{"x": strings.Repeat("y", 5000)},
		"isError":           true,
		"_meta":             map[string]any{"note": "meta-tail"},
	}
	truncated, changed := truncateMCPResultValue(result, maxBytes)
	if !changed {
		t.Fatal("expected the oversized result to be truncated")
	}
	m := mapFromAny(truncated)
	if m == nil {
		t.Fatal("truncated result is not a map")
	}
	if _, ok := m["structuredContent"]; ok {
		t.Fatal("structured content should be dropped")
	}
	if _, ok := m["_meta"]; ok {
		t.Fatal("meta should be dropped")
	}
	if m["isError"] != true {
		t.Fatalf("isError = %#v, want true", m["isError"])
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(encoded) > maxBytes {
		t.Fatalf("serialized result len = %d, want <= %d", len(encoded), maxBytes)
	}
	content, _ := m["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content has %d items, want 1 preview", len(content))
	}
	preview, _ := mapFromAny(content[0])["text"].(string)
	if !strings.Contains(preview, "head") || !strings.Contains(preview, "chars truncated") {
		t.Fatalf("preview = %q", preview)
	}
	if _, again := truncateMCPResultValue(m, maxBytes); again {
		t.Fatal("re-truncating a bounded result should be a no-op")
	}
}

// TestApplyPersistedItemTruncationCapsMCPResult confirms the 64 KiB persisted
// cap is wired into the paginated ItemCompleted path.
func TestApplyPersistedItemTruncationCapsMCPResult(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"type": "McpToolCall",
		"id":   "mcp-1",
		"result": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": strings.Repeat("z", persistedMCPResultMaxBytes+100)}},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := applyPersistedItemTruncation(raw)
	var item map[string]any
	if err := json.Unmarshal(out, &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	result := mapFromAny(item["result"])
	if result == nil {
		t.Fatal("result missing after truncation")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if len(encoded) > persistedMCPResultMaxBytes {
		t.Fatalf("serialized result len = %d, want <= %d", len(encoded), persistedMCPResultMaxBytes)
	}
}
