package features

import "testing"

// Persistent mode enablement is centralized on the reasoning effort selection
// (Rust #48611's `Features::persistent_mode_enabled`).
func TestPersistentModeEnabledMatchesRust(t *testing.T) {
	for _, effort := range []string{"persistent", "Persistent", " PERSISTENT ", "\tpersistent\n"} {
		if !PersistentModeEnabled(effort) {
			t.Fatalf("PersistentModeEnabled(%q) = false, want true", effort)
		}
	}
	for _, effort := range []string{"", " ", "medium", "high", "disabled", "ultra", "persistent-ish"} {
		if PersistentModeEnabled(effort) {
			t.Fatalf("PersistentModeEnabled(%q) = true, want false", effort)
		}
	}
}
