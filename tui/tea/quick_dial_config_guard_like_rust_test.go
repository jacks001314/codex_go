package tea

import (
	"testing"

	"codex_go/config"
)

func TestQuickDialConfigLayerGuardLikeRust(t *testing.T) {
	writes := 0
	model := NewModel(quickDialTestState(), Options{
		Width:  100,
		Height: 30,
		QuickDials: map[string]config.TUIPreset{
			"3": {Input: "managed prompt", Label: quickDialPtr("Managed")},
		},
		PresetSetReadOnly:   map[string]bool{"3": true},
		PresetClearReadOnly: map[string]bool{"3": true},
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes++
			return SettingsWriteResult{}, nil
		},
	})
	if cmd := model.writeQuickDial(3, &config.TUIPreset{Input: "user prompt"}); cmd != nil {
		t.Fatalf("read-only set returned a write command: %v", cmd)
	}
	if writes != 0 {
		t.Fatalf("read-only set wrote settings %d times", writes)
	}
	if want := "Preset 3 is defined outside your user config. Edit its source configuration instead."; model.notice != want {
		t.Fatalf("set notice = %q, want %q", model.notice, want)
	}
	if model.modal == nil || model.modal.id != quickDialEditorID {
		t.Fatalf("read-only set did not restore the editor: %#v", model.modal)
	}

	model.modal = nil
	if cmd := model.writeQuickDial(3, nil); cmd != nil {
		t.Fatalf("read-only clear returned a write command: %v", cmd)
	}
	if writes != 0 {
		t.Fatalf("read-only clear wrote settings %d times", writes)
	}
	if want := "Preset 3 is defined outside your user config. Edit its source configuration instead."; model.notice != want {
		t.Fatalf("clear notice = %q, want %q", model.notice, want)
	}
}

func TestQuickDialConfigLayerGuardAllowsUserOwnedSlot(t *testing.T) {
	writes := 0
	model := NewModel(quickDialTestState(), Options{
		Width:               100,
		Height:              30,
		PresetSetReadOnly:   map[string]bool{"4": false},
		PresetClearReadOnly: map[string]bool{"4": false},
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes++
			return SettingsWriteResult{QuickDials: map[string]config.TUIPreset{"4": {Input: "user"}}}, nil
		},
	})
	if cmd := model.writeQuickDial(4, &config.TUIPreset{Input: "user"}); cmd == nil {
		t.Fatal("user-owned set did not request a write")
	} else {
		_ = cmd()
	}
	if writes != 1 {
		t.Fatalf("user-owned set wrote %d times, want 1", writes)
	}
}
