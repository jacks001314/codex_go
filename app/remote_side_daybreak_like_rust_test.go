package app

import (
	"testing"

	"codex_go/cli"
	codextui "codex_go/tui"
)

// Rust side_fork_config (#60319) disables Daybreak on every side fork, so a
// launch override cannot re-enable it for the side conversation.
func TestRemoteSideForkDisablesDaybreakLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	for _, root := range []*cli.RootOptions{
		{},
		{ConfigOverrides: []string{"daybreak=true"}},
	} {
		params, err := remoteSideThreadForkParams(root, state, "thread-parent", nil)
		if err != nil {
			t.Fatalf("remoteSideThreadForkParams(%v) error = %v", root.ConfigOverrides, err)
		}
		if got, ok := params.Config["daybreak"]; !ok || got != false {
			t.Fatalf("side fork config daybreak = %#v, want false", got)
		}
	}
}
