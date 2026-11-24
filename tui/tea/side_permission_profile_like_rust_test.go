package tea

import (
	"testing"

	"codex_go/tui/keymsg"
)

// Rust #59767: the sidebar fork hands the parent conversation's named
// permission profile to the host, so the side chat starts under the profile
// the parent had even though the parent's Sandbox field still carries the
// resolved mode.
func TestSidebarStartInheritsPermissionProfileLikeRust(t *testing.T) {
	model, started := newSidebarTabsModel(t)
	model.State.ActivePermissionProfile = "side-reader"
	model.State.Sandbox = "workspace-write"

	if cmd := model.focusSidebarContent(); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	_, cmd := model.Update(key(keymsg.KeyEnter))
	runTeaCmd(t, model, cmd)

	if len(*started) != 1 {
		t.Fatalf("side starters = %d, want 1", len(*started))
	}
	params := (*started)[0]
	if params.PermissionProfile != "side-reader" {
		t.Fatalf("side start permission profile = %q, want side-reader", params.PermissionProfile)
	}
	if params.Sandbox != "workspace-write" {
		t.Fatalf("side start sandbox = %q, want the parent's resolved mode", params.Sandbox)
	}
}
