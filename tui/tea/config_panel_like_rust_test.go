package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func configPanelModel(t *testing.T, write func([]SettingsEdit) (SettingsWriteResult, error)) *Model {
	t.Helper()
	return NewModel(codextui.NewState(nil), Options{
		Width:           100,
		Height:          30,
		OnWriteSettings: write,
	})
}

func configPanelRunes(text string) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune(text)}
}

func configPanelRowByLabel(t *testing.T, label string) configPanelRow {
	t.Helper()
	for _, row := range configPanelRows {
		if row.label == label {
			return row
		}
	}
	t.Fatalf("config row %q not found", label)
	return configPanelRow{}
}

func TestConfigCommandOpensSearchablePanelLikeRust(t *testing.T) {
	model := configPanelModel(t, nil)
	invocation, ok := codextui.ParseCommand("/config")
	if !ok {
		t.Fatal("ParseCommand(/config) failed")
	}
	model.applyCommand(invocation)
	if model.modal == nil || model.modal.kind != ModalKindConfig || model.modal.configPanel == nil {
		t.Fatalf("config modal = %#v", model.modal)
	}
	view := utils.StripANSI(model.View())
	for _, want := range []string{
		"Configuration",
		"Appearance",
		"Notifications",
		"Input",
		"Sessions",
		"Theme",
		"Effects",
		"Status line",
		"Terminal title",
		"type to search · ←/→ tabs · enter open · esc close",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("config panel missing %q:\n%s", want, view)
		}
	}

	model.Update(key(bubbletea.KeyRight))
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "Notify when") || strings.Contains(view, "Terminal title") {
		t.Fatalf("tab switch did not show notifications rows:\n%s", view)
	}
	model.Update(key(bubbletea.KeyLeft))
	model.Update(key(bubbletea.KeyRight))
	model.Update(key(bubbletea.KeyRight))
	model.Update(configPanelRunes("keyboard"))
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "Keyboard") || strings.Contains(view, "Right-click paste") {
		t.Fatalf("config search did not filter input rows:\n%s", view)
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.modal != nil {
		t.Fatal("Esc did not close the config panel")
	}
}

func TestConfigPanelEditorsAndPreferenceCyclesLikeRust(t *testing.T) {
	var edits []SettingsEdit
	model := configPanelModel(t, func(next []SettingsEdit) (SettingsWriteResult, error) {
		edits = append([]SettingsEdit(nil), next...)
		result := SettingsWriteResult{FilePath: "/tmp/config.toml"}
		if edits[0].KeyPath == "tui.animations" {
			value, _ := edits[0].Value.(bool)
			result.AnimationsEnabled = &value
		}
		return result, nil
	})

	model.openConfigPanel()
	runTeaCmd(t, model, model.activateConfigRow(configPanelRowByLabel(t, "Theme")))
	if model.modal == nil || model.modal.id != "theme" {
		t.Fatalf("Theme did not open the theme picker: %#v", model.modal)
	}

	model.openConfigPanel()
	runTeaCmd(t, model, model.activateConfigRow(configPanelRowByLabel(t, "Effects")))
	if model.modal == nil || model.modal.configPanel == nil || !model.modal.configPanel.effects {
		t.Fatalf("Effects did not open its sub-picker: %#v", model.modal)
	}
	effectRows := model.configPanelFilteredRows()
	if len(effectRows) != 2 || effectRows[0].label != "Animations" || effectRows[1].label != "Composer stars" {
		t.Fatalf("effects rows = %#v", effectRows)
	}
	runTeaCmd(t, model, model.activateConfigRow(effectRows[0]))
	if len(edits) != 1 || edits[0].KeyPath != "tui.animations" || edits[0].Value != false {
		t.Fatalf("effects write = %#v", edits)
	}
	if model.animationsEnabled {
		t.Fatal("effects toggle did not turn animations off")
	}
	edits = nil
	model.notice = ""
	runTeaCmd(t, model, model.activateConfigRow(effectRows[1]))
	if len(edits) != 0 || !strings.Contains(model.notice, "Turn on Animations") {
		t.Fatalf("disabled composer stars = edits:%#v notice:%q", edits, model.notice)
	}
	runTeaCmd(t, model, model.activateConfigRow(effectRows[0]))
	if !model.animationsEnabled {
		t.Fatal("animations did not turn back on")
	}
	edits = nil
	runTeaCmd(t, model, model.activateConfigRow(effectRows[1]))
	if len(edits) != 1 || edits[0].KeyPath != "tui.effects.starfield" || edits[0].Value != false {
		t.Fatalf("composer stars write = %#v", edits)
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.modal == nil || model.modal.configPanel == nil || model.modal.configPanel.effects {
		t.Fatalf("Esc should return from Effects to Configuration: %#v", model.modal)
	}

	model.openConfigPanel()
	runTeaCmd(t, model, model.activateConfigRow(configPanelRowByLabel(t, "Notify when")))
	if len(edits) != 1 || edits[0].KeyPath != "tui.notification_condition" ||
		edits[0].Value != string(codextui.NotificationConditionAlways) {
		t.Fatalf("notification write = %#v", edits)
	}
	if model.notificationCondition != codextui.NotificationConditionAlways {
		t.Fatalf("notification condition = %q", model.notificationCondition)
	}

	model.openConfigPanel()
	runTeaCmd(t, model, model.activateConfigRow(configPanelRowByLabel(t, "Right-click paste")))
	if len(edits) != 1 || edits[0].KeyPath != "tui.right_click_paste" || edits[0].Value != "on" {
		t.Fatalf("right-click write = %#v", edits)
	}

	model.openConfigPanel()
	runTeaCmd(t, model, model.activateConfigRow(configPanelRowByLabel(t, "Session picker layout")))
	if len(edits) != 1 || edits[0].KeyPath != "tui.session_picker_view" {
		t.Fatalf("session picker write = %#v", edits)
	}

	edits = nil
	model.openConfigPanel()
	model.Update(key(bubbletea.KeyRight))
	model.Update(key(bubbletea.KeyRight))
	model.Update(key(bubbletea.KeyRight))
	model.Update(key(bubbletea.KeyRight))
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "Copy on select") || !strings.Contains(view, "Deselect after copy") {
		t.Fatalf("text selection tab missing rows:\n%s", view)
	}
	rows := model.configPanelFilteredRows()
	runTeaCmd(t, model, model.activateConfigRow(rows[0]))
	if len(edits) != 1 || edits[0].KeyPath != "tui.copy_on_select" || edits[0].Value != "always" {
		t.Fatalf("copy-on-select write = %#v", edits)
	}
	edits = nil
	runTeaCmd(t, model, model.activateConfigRow(rows[1]))
	if len(edits) != 1 || edits[0].KeyPath != "tui.copy_on_select_clear_selection" || edits[0].Value != true {
		t.Fatalf("clear-after-copy write = %#v", edits)
	}
}

func TestConfigPanelRemainingTogglesLikeRust(t *testing.T) {
	var edits []SettingsEdit
	var posted string
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 30,
		OnWriteSettings: func(next []SettingsEdit) (SettingsWriteResult, error) {
			edits = append([]SettingsEdit(nil), next...)
			return SettingsWriteResult{FilePath: "/tmp/config.toml"}, nil
		},
		OnPostNotification: func(message string, method codextui.NotificationMethod) bubbletea.Cmd {
			posted = message
			return nil
		},
	})
	activate := func(label string) {
		t.Helper()
		edits = nil
		model.openConfigPanel()
		runTeaCmd(t, model, model.activateConfigRow(configPanelRowByLabel(t, label)))
	}

	activate("Tips")
	if len(edits) != 1 || edits[0].KeyPath != "tui.show_tooltips" || edits[0].Value != true || !model.showTooltips {
		t.Fatalf("tips toggle = %#v show=%v", edits, model.showTooltips)
	}
	activate("Status line colors")
	if len(edits) != 1 || edits[0].KeyPath != "tui.status_line_use_colors" || edits[0].Value != false {
		t.Fatalf("status colors toggle = %#v", edits)
	}
	activate("Notifications")
	if len(edits) != 1 || edits[0].KeyPath != "tui.notifications" || edits[0].Value != false {
		t.Fatalf("notifications toggle = %#v", edits)
	}
	activate("Paste detection")
	if len(edits) != 1 || edits[0].KeyPath != "tui.disable_paste_burst" || edits[0].Value != true {
		t.Fatalf("paste detection toggle = %#v", edits)
	}
	activate("Start in Vim mode")
	if len(edits) != 1 || edits[0].KeyPath != "tui.vim_mode_default" || edits[0].Value != true || !model.vimModeDefault {
		t.Fatalf("start-in-vim toggle = %#v default=%v", edits, model.vimModeDefault)
	}
	activate("Automatic recaps")
	if len(edits) != 1 || edits[0].KeyPath != "tui.auto_recap" || edits[0].Value != false {
		t.Fatalf("automatic recaps toggle = %#v", edits)
	}
	activate("Deselect after copy")
	if len(edits) != 1 || edits[0].KeyPath != "tui.copy_on_select_clear_selection" || edits[0].Value != true {
		t.Fatalf("deselect toggle = %#v", edits)
	}

	model.copyOnSelectMode = "never"
	edits = nil
	model.notice = ""
	runTeaCmd(t, model, model.activateConfigRow(configPanelRowByLabel(t, "Deselect after copy")))
	if len(edits) != 0 || !strings.Contains(model.notice, "Turn on Copy on select") {
		t.Fatalf("dependent deselect = edits:%#v notice:%q", edits, model.notice)
	}

	model.openConfigPanel()
	if cmd := model.activateConfigRow(configPanelRowByLabel(t, "Test notification")); cmd == nil {
		t.Fatal("test notification did not schedule a delayed command")
	}
	runTeaCmd(t, model, model.postConfigTestNotification())
	if posted != "Test notification" {
		t.Fatalf("test notification = %q", posted)
	}
}

func TestConfigPanelSaveErrorKeepsPanelAndExplainsRetryLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 30,
		OnWriteSettings: func([]SettingsEdit) (SettingsWriteResult, error) {
			return SettingsWriteResult{}, errors.New("failed to parse config.toml: expected `]`")
		},
	})
	model.openConfigPanel()
	runTeaCmd(t, model, model.activateConfigRow(configPanelRowByLabel(t, "Tips")))
	if model.modal == nil || model.modal.configPanel == nil {
		t.Fatal("save failure closed the config panel")
	}
	if !strings.Contains(model.notice, "Failed to save configuration: failed to parse config.toml") ||
		!strings.Contains(model.notice, "The task can continue") {
		t.Fatalf("save failure notice = %q", model.notice)
	}
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "Failed to save configuration: failed to parse config.toml") {
		t.Fatalf("save failure is not visible in the open panel:\n%s", view)
	}
}

func TestConfigPanelSaveNoticesLikeRust(t *testing.T) {
	overridden := configPanelModel(t, func([]SettingsEdit) (SettingsWriteResult, error) {
		return SettingsWriteResult{ShowTooltips: boolPtrTea(false), FilePath: "/tmp/config.toml"}, nil
	})
	overridden.openConfigPanel()
	runTeaCmd(t, overridden, overridden.activateConfigRow(configPanelRowByLabel(t, "Tips")))
	wantOverride := "Saved · overridden by config or system setting"
	if overridden.notice != wantOverride || overridden.modal.configPanel.notice != wantOverride {
		t.Fatalf("override notice = %q panel:%q", overridden.notice, overridden.modal.configPanel.notice)
	}
	if view := utils.StripANSI(overridden.View()); !strings.Contains(view, wantOverride) {
		t.Fatalf("override notice is not visible in the panel:\n%s", view)
	}

	vim := configPanelModel(t, func([]SettingsEdit) (SettingsWriteResult, error) {
		return SettingsWriteResult{StartInVimMode: boolPtrTea(true), FilePath: "/tmp/config.toml"}, nil
	})
	vim.openConfigPanel()
	runTeaCmd(t, vim, vim.activateConfigRow(configPanelRowByLabel(t, "Start in Vim mode")))
	wantVim := "Saved · applies to new composers"
	if vim.notice != wantVim || vim.modal.configPanel.notice != wantVim {
		t.Fatalf("vim notice = %q panel:%q", vim.notice, vim.modal.configPanel.notice)
	}

	plain := configPanelModel(t, func([]SettingsEdit) (SettingsWriteResult, error) {
		return SettingsWriteResult{ShowTooltips: boolPtrTea(true), FilePath: "/tmp/config.toml"}, nil
	})
	plain.openConfigPanel()
	runTeaCmd(t, plain, plain.activateConfigRow(configPanelRowByLabel(t, "Tips")))
	if plain.notice != "" || plain.modal.configPanel.notice != "" {
		t.Fatalf("plain save notice = %q panel:%q", plain.notice, plain.modal.configPanel.notice)
	}
	if view := utils.StripANSI(plain.View()); !strings.Contains(view, "Changes save automatically") {
		t.Fatalf("plain save did not restore the default subtitle:\n%s", view)
	}
}
