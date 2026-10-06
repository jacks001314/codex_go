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
