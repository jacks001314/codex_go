package chatwidget

import (
	"testing"

	bottompane "codex_go/tui/bottom_pane"
)

func TestKeymapReplaceBindingMenuMatchesRust(t *testing.T) {
	view := NewKeymapReplaceBindingMenuView(KeymapActionItem{
		Action:   "copy",
		Bindings: []string{"ctrl+o", "alt+c"},
	})
	if view.ViewID != KeymapReplaceBindingMenuViewID || len(view.Items) != 2 {
		t.Fatalf("replace view = %#v", view)
	}
	if view.Items[0].Name != "ctrl+o" || view.Items[0].Action != KeymapActionReplaceBinding || !view.Items[0].DismissOnSelect {
		t.Fatalf("first replace item = %#v", view.Items[0])
	}

	empty := NewKeymapReplaceBindingMenuView(KeymapActionItem{Action: "copy"})
	if len(empty.Items) != 1 || !empty.Items[0].Disabled || empty.Items[0].Name != "No bindings configured" {
		t.Fatalf("empty replace view = %#v", empty)
	}
}

func TestApplyKeymapRuntimeUpdateSynchronizesBindingsMatchRust(t *testing.T) {
	result := ApplyKeymapRuntimeUpdate(KeymapRuntimeBindings{
		AppCopyLastResponse:   " ctrl+o ",
		ChatEditQueuedMessage: " alt+up ",
	})
	if result.CopyLastResponseBinding != "ctrl+o" ||
		result.ChatEditQueuedBinding != "alt+up" ||
		!result.BottomPaneBindingsUpdated ||
		!result.RequestRedraw {
		t.Fatalf("apply result = %#v", result)
	}
}

func TestKeymapActionMenuResponsiveConfigMatchesRust(t *testing.T) {
	custom := false
	view := NewKeymapActionMenuView(KeymapActionItem{
		Action:           "open_transcript",
		Bindings:         []string{"ctrl-t"},
		HasCustomBinding: &custom,
	})
	if view.ColumnWidth.Mode != bottompane.ColumnWidthAutoAllRows {
		t.Fatalf("column width mode = %v, want AutoAllRows", view.ColumnWidth.Mode)
	}
	// Rust #46691's picker preset hides narrow descriptions instead of stacking them.
	if view.DescriptionLayout.Mode != bottompane.SelectionDescriptionHideWhenNarrow || view.DescriptionLayout.MinDescriptionWidth != 24 {
		t.Fatalf("description layout = %#v", view.DescriptionLayout)
	}
	if len(view.Items) != 6 || view.Items[4].Name != "Remove custom binding" || !view.Items[4].Disabled || view.Items[4].DisabledGutterMarker != "–" {
		t.Fatalf("action menu items = %#v", view.Items)
	}
	if view.Items[4].DisabledReason != "No custom root override to remove." {
		t.Fatalf("remove disabled reason = %q", view.Items[4].DisabledReason)
	}
}

// Rust #52273 (keymap_setup.rs:252-278): every action except global.leader
// offers a leader-chord capture, and an unbound leader adds the entry that
// navigates to the leader action so it can be configured first.
func TestKeymapActionMenuShowsLeaderEntriesLikeRust(t *testing.T) {
	unbound := NewKeymapActionMenuView(KeymapActionItem{Action: "open_transcript", Bindings: []string{"ctrl-t"}})
	if len(unbound.Items) != 6 || unbound.Items[2].Name != "Use leader..." || unbound.Items[3].Name != "Configure leader..." {
		t.Fatalf("leader entries = %#v", unbound.Items)
	}
	if !unbound.Items[2].Disabled || unbound.Items[2].DisabledReason != "The leader is unbound" || unbound.Items[2].DisabledGutterMarker != "–" {
		t.Fatalf("disabled leader entry = %#v", unbound.Items[2])
	}

	active := NewKeymapActionMenuView(KeymapActionItem{Action: "open_transcript", Bindings: []string{"ctrl-t"}, LeaderAvailable: true})
	if len(active.Items) != 5 || active.Items[2].Name != "Use leader..." || active.Items[2].Disabled {
		t.Fatalf("active leader entries = %#v", active.Items)
	}

	leaderRow := NewKeymapActionMenuView(KeymapActionItem{Action: "leader", Bindings: []string{"ctrl-x"}, LeaderAvailable: true, IsLeaderAction: true})
	if len(leaderRow.Items) != 4 {
		t.Fatalf("leader row items = %#v", leaderRow.Items)
	}
	for _, item := range leaderRow.Items {
		if item.Name == "Use leader..." || item.Name == "Configure leader..." {
			t.Fatalf("the leader action offered a leader entry: %#v", leaderRow.Items)
		}
	}
}
