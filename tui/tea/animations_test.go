package tea

import (
	"testing"

	codextui "codex_go/tui"
)

func TestModelAnimationsSettingDefaultsToEnabled(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{})
	if !model.animationsEnabled {
		t.Fatal("animations should default to enabled")
	}

	disabled := false
	off := NewModel(codextui.NewState(nil), Options{AnimationsEnabled: &disabled})
	if off.animationsEnabled {
		t.Fatal("an explicit disabled animations preference must be respected")
	}

	enabled := true
	on := NewModel(codextui.NewState(nil), Options{AnimationsEnabled: &enabled})
	if !on.animationsEnabled {
		t.Fatal("an explicit enabled animations preference must be respected")
	}
}

func TestSettingsWriteResultAppliesAnimationsSetting(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{})
	disabled := false
	model.Update(SettingsWriteResultMsg{Result: SettingsWriteResult{AnimationsEnabled: &disabled}})
	if model.animationsEnabled {
		t.Fatal("a settings result should disable animations")
	}

	enabled := true
	model.Update(SettingsWriteResultMsg{Result: SettingsWriteResult{AnimationsEnabled: &enabled}})
	if !model.animationsEnabled {
		t.Fatal("a settings result should re-enable animations")
	}
}
