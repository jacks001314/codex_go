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
	if view.DescriptionLayout.Mode != bottompane.SelectionDescriptionStackBelowWhenNarrow || view.DescriptionLayout.MinDescriptionWidth != 24 {
		t.Fatalf("description layout = %#v", view.DescriptionLayout)
	}
	if len(view.Items) != 4 || view.Items[2].Name != "Remove custom binding" || !view.Items[2].Disabled || view.Items[2].DisabledGutterMarker != "–" {
		t.Fatalf("action menu items = %#v", view.Items)
	}
	if view.Items[2].DisabledReason != "No custom root override to remove." {
		t.Fatalf("remove disabled reason = %q", view.Items[2].DisabledReason)
	}
}
