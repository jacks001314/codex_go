package tea

import (
	"os"
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

// Rust parity: codex-rs/tui/src/config_panel (mouse_scroll_speed editor,
// ConfigPreference::MouseScrollSpeed) and app/session_lifecycle.rs refreshing
// config_panel_tui.mouse_scroll_speed from local settings (#61976).

func mouseScrollSpeedPointer(value float64) *float64 { return &value }

func mouseScrollChoice(t *testing.T, model *Model) *configPanelChoice {
	t.Helper()
	if model.modal == nil || model.modal.configPanel == nil || model.modal.configPanel.choice == nil {
		t.Fatal("mouse scroll speed picker is not open")
	}
	return model.modal.configPanel.choice
}

// TestMouseScrollSpeedConfigPickerLikeRust covers Rust's picker contract: the
// presets stay sorted, a custom configured value remains selectable as the
// current row, and the default is labelled.
func TestMouseScrollSpeedConfigPickerLikeRust(t *testing.T) {
	var edits []SettingsEdit
	model := NewModel(codextui.NewState(nil), Options{
		Width:            100,
		Height:           30,
		MouseScrollSpeed: mouseScrollSpeedPointer(1.75),
		OnWriteSettings: func(next []SettingsEdit) (SettingsWriteResult, error) {
			edits = append([]SettingsEdit(nil), next...)
			return SettingsWriteResult{MouseScrollSpeed: mouseScrollSpeedPointer(2.0), FilePath: "/tmp/config.toml"}, nil
		},
	})
	model.openConfigPanel()
	model.activateConfigRow(configPanelRowByLabel(t, "Mouse scroll speed"))
	choice := mouseScrollChoice(t, model)
	wantLabels := []string{"0.5×", "1× (default)", "1.5×", "1.75×", "2×", "3×"}
	if len(choice.items) != len(wantLabels) {
		t.Fatalf("picker items = %#v", choice.items)
	}
	for index, want := range wantLabels {
		if choice.items[index].label != want {
			t.Fatalf("item %d = %q, want %q", index, choice.items[index].label, want)
		}
	}
	if choice.selected != 3 {
		t.Fatalf("initial selection = %d, want the current 1.75x row", choice.selected)
	}
	if !choice.items[3].current {
		t.Fatalf("custom 1.75x is not marked current: %#v", choice.items[3])
	}
	// The picker renders the current row with Rust's "(current)" suffix.
	if view := utils.StripANSI(model.viewString()); !strings.Contains(view, "1.75× (current)") {
		t.Fatalf("picker does not mark the custom value as current:\n%s", view)
	}
	model.updateConfigPanelChoiceKey(key(keymsg.KeyDown))
	runTeaCmd(t, model, model.updateConfigPanelChoiceKey(key(keymsg.KeyEnter)))
	if len(edits) != 1 || edits[0].KeyPath != "tui.mouse_scroll_speed" || edits[0].Value != 2.0 {
		t.Fatalf("saved edits = %#v", edits)
	}
	if got := model.MouseScrollSpeed(); got != 2.0 {
		t.Fatalf("live mouse scroll speed = %v, want 2.0", got)
	}
}

// TestMouseScrollSpeedPanelRowLikeRust covers the Input row label: the panel
// shows the configured multiplier rather than a generic editor value.
func TestMouseScrollSpeedPanelRowLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:            100,
		Height:           30,
		MouseScrollSpeed: mouseScrollSpeedPointer(1.75),
	})
	model.openConfigPanel()
	model.updateConfigPanelKey(configPanelRunes("/"))
	model.updateConfigPanelKey(configPanelRunes("mouse scroll speed"))
	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, "Mouse scroll speed") || !strings.Contains(view, "1.75×") {
		t.Fatalf("config panel row missing the configured speed:\n%s", view)
	}
}

// TestMouseScrollSpeedReloadRepricesPickerLikeRust covers a settings reload
// (the host's config read) reaching both the live transcript and the picker.
func TestMouseScrollSpeedReloadRepricesPickerLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	model.applyLocalSettingsValues(SettingsWriteResult{MouseScrollSpeed: mouseScrollSpeedPointer(1.5)})
	if got := model.MouseScrollSpeed(); got != 1.5 {
		t.Fatalf("live mouse scroll speed = %v, want 1.5", got)
	}
	model.openConfigPanel()
	model.activateConfigRow(configPanelRowByLabel(t, "Mouse scroll speed"))
	choice := mouseScrollChoice(t, model)
	if !choice.items[choice.selected].current || choice.items[choice.selected].label != "1.5×" {
		t.Fatalf("current picker row = %#v, want 1.5× marked current", choice.items[choice.selected])
	}
}

// TestChangingDirectoryReloadsLocalSettingsLikeRust covers Rust's
// session_lifecycle copy: the replacement session adopts the destination
// directory's local settings, so its config panel reflects them.
func TestChangingDirectoryReloadsLocalSettingsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	sourceCWD := t.TempDir()
	targetCWD := t.TempDir()
	state.CWD = sourceCWD
	var reloadedFor string
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnWorkingDirectoryChange: func(threadID string, cwd string) (*codextui.SessionSummary, error) {
			return &codextui.SessionSummary{ThreadID: "thread-target", CWD: cwd}, nil
		},
		OnReloadSettingsForCWD: func(cwd string) (SettingsWriteResult, error) {
			reloadedFor = cwd
			return SettingsWriteResult{MouseScrollSpeed: mouseScrollSpeedPointer(1.75)}, nil
		},
	})
	command := model.applyWorkingDirectoryChangeCommand(targetCWD)
	runTeaCmd(t, model, command)
	if reloadedFor != targetCWD {
		t.Fatalf("settings reloaded for %q, want %q", reloadedFor, targetCWD)
	}
	if got := model.MouseScrollSpeed(); got != 1.75 {
		t.Fatalf("live mouse scroll speed = %v, want the destination's 1.75", got)
	}
	if model.localSettings.Tui.MouseScrollSpeed == nil || *model.localSettings.Tui.MouseScrollSpeed != 1.75 {
		t.Fatalf("staged local settings did not adopt the destination value: %#v", model.localSettings.Tui.MouseScrollSpeed)
	}
	model.openConfigPanel()
	model.updateConfigPanelKey(configPanelRunes("/"))
	model.updateConfigPanelKey(configPanelRunes("mouse scroll speed"))
	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, "1.75×") {
		t.Fatalf("config panel kept the old directory's speed:\n%s", view)
	}
}

// TestChangingDirectoryKeepsLiveSettingsWhenReloadFailsLikeRust covers Rust's
// failed-reload path: the live preferences survive.
func TestChangingDirectoryKeepsLiveSettingsWhenReloadFailsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	state.CWD = t.TempDir()
	targetCWD := t.TempDir()
	model := NewModel(state, Options{
		Width:            100,
		Height:           30,
		MouseScrollSpeed: mouseScrollSpeedPointer(2.0),
		OnWorkingDirectoryChange: func(threadID string, cwd string) (*codextui.SessionSummary, error) {
			return &codextui.SessionSummary{ThreadID: "thread-target", CWD: cwd}, nil
		},
		OnReloadSettingsForCWD: func(string) (SettingsWriteResult, error) {
			return SettingsWriteResult{}, os.ErrPermission
		},
	})
	runTeaCmd(t, model, model.applyWorkingDirectoryChangeCommand(targetCWD))
	if got := model.MouseScrollSpeed(); got != 2.0 {
		t.Fatalf("failed reload changed the live speed: %v", got)
	}
}
