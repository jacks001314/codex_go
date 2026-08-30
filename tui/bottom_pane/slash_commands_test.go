package bottompane

import "testing"

// TestServiceTierCommandsFromIDsLikeRust pins the model-catalog service-tier ID
// -> slash-command mapping (default is implicit, "priority" is named "fast").
func TestServiceTierCommandsFromIDsLikeRust(t *testing.T) {
	commands := ServiceTierCommandsFromIDs([]string{"priority", "default", ""})
	if len(commands) != 1 || commands[0].ID != "priority" || commands[0].Name != "fast" {
		t.Fatalf("ServiceTierCommandsFromIDs = %#v, want lone fast command", commands)
	}
	if got := ServiceTierCommandsFromIDs([]string{"default", ""}); len(got) != 0 {
		t.Fatalf("default-only service tiers = %#v, want empty", got)
	}
}
