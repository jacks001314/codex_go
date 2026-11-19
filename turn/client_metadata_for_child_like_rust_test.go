package turn

import "testing"

// Mirrors Rust TurnMetadataState::client_metadata_for_child (#62072): the child
// inherits client attribution but not the parent's review or routing intent, and
// the result is a snapshot taken when the spawn was requested.
func TestClientMetadataForChildFiltersParentIntentLikeRust(t *testing.T) {
	parent := map[string]string{
		"cost_center":           "cc-1",
		"thread_id":             "parent-thread",
		"turn_id":               "parent-turn",
		"mcp_request_meta":      `{"selected_connector_ids":["parent"]}`,
		"parent_response_id":    "parent-response",
		"misalignment_override": `{"review_target":"parent-only"}`,
	}
	snapshot := ClientMetadataForChild(parent)
	want := map[string]string{
		"cost_center": "cc-1",
		"thread_id":   "parent-thread",
		"turn_id":     "parent-turn",
	}
	if len(snapshot) != len(want) {
		t.Fatalf("inherited metadata = %#v, want %#v", snapshot, want)
	}
	for key, value := range want {
		if snapshot[key] != value {
			t.Fatalf("inherited[%q] = %q, want %q", key, snapshot[key], value)
		}
	}
	// A later parent update must not reach the snapshot already handed to a child.
	parent["cost_center"] = "after"
	if snapshot["cost_center"] != "cc-1" {
		t.Fatalf("snapshot changed with the parent: %#v", snapshot)
	}
	if got := ClientMetadataForChild(map[string]string{"parent_response_id": "only"}); got != nil {
		t.Fatalf("metadata made only of dropped keys = %#v, want nil", got)
	}
	if got := ClientMetadataForChild(nil); got != nil {
		t.Fatalf("ClientMetadataForChild(nil) = %#v, want nil", got)
	}
}
