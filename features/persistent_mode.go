package features

import "strings"

// PersistentModeEnabled mirrors Rust `Features::persistent_mode_enabled`
// (#48611): persistent execution is enabled by the persistent reasoning effort,
// and the single decision is shared by the persistent-mode instructions and the
// current-time reminder defaults so the two consumers cannot drift.
func PersistentModeEnabled(reasoningEffort string) bool {
	return strings.EqualFold(strings.TrimSpace(reasoningEffort), "persistent")
}
