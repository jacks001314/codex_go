package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func openEffectsPanel(t *testing.T, model *Model) {
	t.Helper()
	model.openConfigPanel()
	model.activateConfigRow(configPanelRowByLabel(t, "Effects"))
	if model.modal == nil || model.modal.configPanel == nil || !model.modal.configPanel.effects {
		t.Fatalf("Effects sub-picker did not open: %#v", model.modal)
	}
}

// Rust #53496: the Effects list uses the shared checkbox toggle handler, so
// Space toggles the highlighted row just like Enter.
func TestEffectsSpaceTogglesSelectedRowLikeRust(t *testing.T) {
	var edits []SettingsEdit
	model := configPanelModel(t, func(change []SettingsEdit) (SettingsWriteResult, error) {
		edits = append(edits, change...)
		enabled := change[0].Value.(bool)
		return SettingsWriteResult{AnimationsEnabled: &enabled}, nil
	})
	openEffectsPanel(t, model)

	_, cmd := model.Update(key(bubbletea.KeySpace))
	runTeaCmd(t, model, cmd)
	if model.animationsEnabled {
		t.Fatal("Space did not toggle Animations off")
	}
	if len(edits) != 1 || edits[0].KeyPath != "tui.animations" || edits[0].Value != false {
		t.Fatalf("Space toggle write = %#v", edits)
	}
	if model.modal == nil || model.modal.configPanel == nil || !model.modal.configPanel.effects {
		t.Fatal("toggling a row closed the Effects sub-picker")
	}
}

// Rust #53496: a configured list binding takes precedence over Space toggling.
func TestEffectsSpaceHonorsConfiguredCancelBindingLikeRust(t *testing.T) {
	keymapConfig := codextui.NewKeymapConfig()
	if err := keymapConfig.Set("list", "cancel", []string{"space"}); err != nil {
		t.Fatalf("configure list.cancel: %v", err)
	}
	model := NewModel(codextui.NewState(nil), Options{
		Width:        100,
		Height:       30,
		KeymapConfig: keymapConfig,
	})
	openEffectsPanel(t, model)

	model.Update(key(bubbletea.KeySpace))
	if model.modal == nil || model.modal.configPanel == nil || model.modal.configPanel.effects {
		t.Fatalf("Space cancel binding did not return to the main panel: %#v", model.modal)
	}
	if !model.animationsEnabled {
		t.Fatal("Space changed Animations despite the configured cancel binding")
	}
}

// Rust #53496: rapid toggles keep the newest choice when an older save result
// arrives; the stale refresh must not clobber the live value.
func TestEffectsRapidTogglesIgnoreStaleSaveResultLikeRust(t *testing.T) {
	results := []SettingsWriteResult{
		{AnimationsEnabled: boolPtrTea(false)},
		{AnimationsEnabled: boolPtrTea(true)},
	}
	next := 0
	model := configPanelModel(t, func([]SettingsEdit) (SettingsWriteResult, error) {
		if next >= len(results) {
			t.Fatalf("unexpected extra settings write")
		}
		result := results[next]
		next++
		return result, nil
	})
	openEffectsPanel(t, model)

	_, first := model.Update(key(bubbletea.KeySpace))
	_, second := model.Update(key(bubbletea.KeySpace))
	if !model.animationsEnabled {
		t.Fatal("second toggle did not restore Animations on")
	}
	if first == nil || second == nil {
		t.Fatal("rapid toggles did not schedule both saves")
	}

	model.Update(first())
	if !model.animationsEnabled {
		t.Fatal("stale save result turned Animations off")
	}
	if value := model.configPanelValue(model.configPanelFilteredRows()[0]); value != "On" {
		t.Fatalf("Effects row after stale result = %q, want On", value)
	}

	model.Update(second())
	if !model.animationsEnabled {
		t.Fatal("newest save result did not keep Animations on")
	}
}

// Rust #53496: Composer stars keeps its underlying value while Animations
// disables the row, and becomes toggleable again when animations return.
func TestEffectsComposerStarsPendingValueSurvivesDisabledRowLikeRust(t *testing.T) {
	model := configPanelModel(t, func([]SettingsEdit) (SettingsWriteResult, error) {
		return SettingsWriteResult{}, nil
	})
	model.effects.Starfield = true
	model.animationsEnabled = false
	openEffectsPanel(t, model)

	rows := model.configPanelFilteredRows()
	if len(rows) != 2 {
		t.Fatalf("effects rows = %#v", rows)
	}
	if value := model.configPanelValue(rows[1]); value != "Unavailable" {
		t.Fatalf("disabled Composer stars value = %q", value)
	}
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeySpace))
	if !model.effects.Starfield {
		t.Fatal("disabled Composer stars row changed the preserved value")
	}
	if !strings.Contains(model.notice, "Turn on Animations") {
		t.Fatalf("disabled Composer stars notice = %q", model.notice)
	}

	model.Update(key(bubbletea.KeyUp))
	model.Update(key(bubbletea.KeySpace))
	if !model.animationsEnabled {
		t.Fatal("Animations did not turn back on")
	}
	if value := model.configPanelValue(model.configPanelFilteredRows()[1]); value != "On" {
		t.Fatalf("Composer stars after re-enabling animations = %q, want On", value)
	}
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeySpace))
	if model.effects.Starfield {
		t.Fatal("Composer stars Space toggle did not turn the preserved value off")
	}
}
