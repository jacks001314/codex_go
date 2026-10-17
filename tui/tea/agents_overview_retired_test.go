package tea

import "testing"

// TestAgentsOverviewWorktreesEnabledFollowsLocalScopeLikeRust mirrors Rust
// #54175: linked-checkout grouping follows the local workspace, not the retired
// `worktrees` feature flag.
func TestAgentsOverviewWorktreesEnabledFollowsLocalScopeLikeRust(t *testing.T) {
	local := &Model{localSession: true, featureSettings: map[string]bool{"worktrees": false}}
	if !local.agentsOverviewWorktreesEnabled() {
		t.Fatal("local dashboard must group linked checkouts even with the retired flag disabled")
	}
	remote := &Model{localSession: false, featureSettings: map[string]bool{"worktrees": true}}
	if remote.agentsOverviewWorktreesEnabled() {
		t.Fatal("remote dashboard must not group local linked checkouts")
	}
	var nilModel *Model
	if nilModel.agentsOverviewWorktreesEnabled() {
		t.Fatal("nil model must not group")
	}
}
