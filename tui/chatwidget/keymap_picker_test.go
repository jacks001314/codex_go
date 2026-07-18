package chatwidget

import "testing"

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
