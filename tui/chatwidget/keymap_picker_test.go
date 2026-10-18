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
	if len(view.Items) != 8 || view.Items[6].Name != "Remove custom binding" || !view.Items[6].Disabled || view.Items[6].DisabledGutterMarker != "–" {
		t.Fatalf("action menu items = %#v", view.Items)
	}
	if view.Items[6].DisabledReason != "No custom root override to remove." {
		t.Fatalf("remove disabled reason = %q", view.Items[6].DisabledReason)
	}
}

// Rust #52273 (keymap_setup.rs:252-278): every action except global.leader
// offers a leader-chord capture, and an unbound leader adds the entry that
// navigates to the leader action so it can be configured first.
func TestKeymapActionMenuShowsLeaderEntriesLikeRust(t *testing.T) {
	unbound := NewKeymapActionMenuView(KeymapActionItem{Action: "open_transcript", Bindings: []string{"ctrl-t"}})
	if len(unbound.Items) != 8 || unbound.Items[4].Name != "Use leader..." || unbound.Items[5].Name != "Configure leader..." {
		t.Fatalf("leader entries = %#v", unbound.Items)
	}
	if !unbound.Items[4].Disabled || unbound.Items[4].DisabledReason != "The leader is unbound" || unbound.Items[4].DisabledGutterMarker != "–" {
		t.Fatalf("disabled leader entry = %#v", unbound.Items[4])
	}

	active := NewKeymapActionMenuView(KeymapActionItem{Action: "open_transcript", Bindings: []string{"ctrl-t"}, LeaderAvailable: true})
	if len(active.Items) != 7 || active.Items[4].Name != "Use leader..." || active.Items[4].Disabled {
		t.Fatalf("active leader entries = %#v", active.Items)
	}

	leaderRow := NewKeymapActionMenuView(KeymapActionItem{Action: "leader", Bindings: []string{"ctrl-x"}, LeaderAvailable: true, IsLeaderAction: true})
	if len(leaderRow.Items) != 6 {
		t.Fatalf("leader row items = %#v", leaderRow.Items)
	}
	for _, item := range leaderRow.Items {
		if item.Name == "Use leader..." || item.Name == "Configure leader..." {
			t.Fatalf("the leader action offered a leader entry: %#v", leaderRow.Items)
		}
	}
}

// TestKeymapActionMenuShowsChordEntriesLikeRust covers #46691's two
// two-stroke chord actions for bound and unbound shortcuts.
func TestKeymapActionMenuShowsChordEntriesLikeRust(t *testing.T) {
	bound := NewKeymapActionMenuView(KeymapActionItem{
		Action:   "open_transcript",
		Bindings: []string{"ctrl-t"},
	})
	names := map[string]bool{}
	for _, item := range bound.Items {
		names[item.Name] = true
	}
	if !names["Replace with key chord"] || !names["Add alternate key chord"] {
		t.Fatalf("bound chord entries = %#v", names)
	}
	unbound := NewKeymapActionMenuView(KeymapActionItem{Action: "unbound"})
	names = map[string]bool{}
	for _, item := range unbound.Items {
		names[item.Name] = true
	}
	if !names["Set key chord"] {
		t.Fatalf("unbound chord entries = %#v", names)
	}
}
