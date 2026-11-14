package tea

import (
	"errors"
	"testing"

	"codex_go/config"
	codextui "codex_go/tui"
)

func TestSaveStatusLineProviderStagesSuccessfulReloadLikeRust(t *testing.T) {
	selection := config.StatusLineProviderSelection{Server: "work", Tool: "usage"}
	var edits []SettingsEdit
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 30,
		OnWriteSettings: func(received []SettingsEdit) (SettingsWriteResult, error) {
			edits = append([]SettingsEdit(nil), received...)
			return SettingsWriteResult{StatusLineProvider: &selection, FilePath: "config.toml"}, nil
		},
	})
	cmd := model.SaveStatusLineProvider(selection)
	if cmd == nil {
		t.Fatal("SaveStatusLineProvider returned no command")
	}
	model.Update(cmd())
	if len(edits) != 1 || edits[0].KeyPath != statusLineProviderConfigKey {
		t.Fatalf("edits = %#v", edits)
	}
	value, _ := edits[0].Value.(map[string]any)
	if value["server"] != "work" || value["tool"] != "usage" {
		t.Fatalf("edit value = %#v", edits[0].Value)
	}
	live := model.StatusLineProvider()
	if live == nil || live.Server != "work" || live.Tool != "usage" {
		t.Fatalf("live selection = %#v", live)
	}
}

func TestSaveStatusLineProviderFailureKeepsLiveValueLikeRust(t *testing.T) {
	previous := config.StatusLineProviderSelection{Default: true}
	model := NewModel(codextui.NewState(nil), Options{
		Width:              100,
		Height:             30,
		StatusLineProvider: &previous,
		OnWriteSettings: func(received []SettingsEdit) (SettingsWriteResult, error) {
			return SettingsWriteResult{StatusLineProvider: &config.StatusLineProviderSelection{Server: "work", Tool: "usage"}}, errors.New("read-only config")
		},
	})
	cmd := model.SaveStatusLineProvider(config.StatusLineProviderSelection{Server: "work", Tool: "usage"})
	if cmd == nil {
		t.Fatal("SaveStatusLineProvider returned no command")
	}
	model.Update(cmd())
	live := model.StatusLineProvider()
	if live == nil || !live.Default {
		t.Fatalf("failed write changed live selection: %#v", live)
	}
}

func TestSaveStatusLineProviderDefaultValue(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		OnWriteSettings: func(received []SettingsEdit) (SettingsWriteResult, error) {
			if len(received) != 1 || received[0].Value != "default" {
				t.Fatalf("default edit = %#v", received)
			}
			return SettingsWriteResult{StatusLineProvider: &config.StatusLineProviderSelection{Default: true}}, nil
		},
	})
	if cmd := model.SaveStatusLineProvider(config.StatusLineProviderSelection{Default: true}); cmd == nil {
		t.Fatal("default selection produced no command")
	} else {
		model.Update(cmd())
	}
}
