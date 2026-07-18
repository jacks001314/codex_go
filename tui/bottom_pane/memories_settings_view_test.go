package bottompane

import (
	"strings"
	"testing"

	"codex_go/tui"
)

func TestMemoriesSettingsToggleAndSaveMatchesRust(t *testing.T) {
	view := NewMemoriesSettingsView(true, false)
	if !view.CurrentSetting(MemoriesSettingUse) || view.CurrentSetting(MemoriesSettingGenerate) {
		t.Fatalf("initial settings use=%v generate=%v", view.UseMemories, view.GenerateMemories)
	}
	rows := view.Rows(120)
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow("\u203a [x] Use memories       Use memories in the following threads. Applied at next thread.")) {
		t.Fatalf("rows missing selected use row:\n%s", strings.Join(rows, "\n"))
	}

	view.HandleKey("space")
	view.HandleKey("down")
	view.HandleKey("space")
	if view.UseMemories || !view.GenerateMemories || view.Enabled {
		t.Fatalf("settings after toggle use=%v generate=%v enabled=%v", view.UseMemories, view.GenerateMemories, view.Enabled)
	}

	view.HandleKey("enter")
	if !view.Complete {
		t.Fatalf("view should complete after save")
	}
	if len(view.Events) != 1 || view.Events[0].Kind != MemoriesEventUpdateSettings || view.Events[0].UseMemories || !view.Events[0].GenerateMemories {
		t.Fatalf("events = %#v", view.Events)
	}
}

func TestMemoriesSettingsResetConfirmationMatchesRust(t *testing.T) {
	view := NewMemoriesSettingsView(true, true)
	view.HandleKey("end")
	view.HandleKey("enter")
	if view.ResetState == nil || view.Complete {
		t.Fatalf("expected reset confirmation, view=%#v", view)
	}
	rows := view.Rows(100)
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow("\u203aReset all memories  Delete local memory files and rollout summaries.")) {
		t.Fatalf("reset rows missing selected reset:\n%s", strings.Join(rows, "\n"))
	}

	view.HandleKey("esc")
	if view.ResetState != nil || view.State.SelectedIdx != 2 || view.Complete {
		t.Fatalf("esc should return to settings, view=%#v", view)
	}

	view.HandleKey("enter")
	view.HandleKey("down")
	view.HandleKey("enter")
	if view.ResetState != nil || view.Complete {
		t.Fatalf("go back should return to settings, view=%#v", view)
	}

	view.HandleKey("enter")
	view.HandleKey("enter")
	if !view.Complete {
		t.Fatalf("reset should complete")
	}
	if len(view.Events) != 1 || view.Events[0].Kind != MemoriesEventReset {
		t.Fatalf("events = %#v", view.Events)
	}
}

func TestMemoriesSettingsCancelCompletesOutsideReset(t *testing.T) {
	view := NewMemoriesSettingsView(false, false)
	view.HandleKey("ctrl+c")
	if !view.Complete || len(view.Events) != 0 {
		t.Fatalf("cancel view=%#v events=%#v", view, view.Events)
	}
}
