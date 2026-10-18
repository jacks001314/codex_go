package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/utils"
)

func tuiModeModel(t *testing.T, write func([]SettingsEdit) (SettingsWriteResult, error), noAltScreen bool) *Model {
	t.Helper()
	return NewModel(codextui.NewState(nil), Options{
		Width:           120,
		Height:          24,
		NoAltScreen:     noAltScreen,
		OnWriteSettings: write,
	})
}

func confirmSelectedTUIMode(t *testing.T, model *Model, down int) bubbletea.Cmd {
	t.Helper()
	model.openTUIModePicker()
	for range down {
		model.Update(key(bubbletea.KeyDown))
	}
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil {
		t.Fatal("first confirmation press closed the picker")
	}
	_, command := model.Update(key(bubbletea.KeyEnter))
	if command == nil {
		t.Fatal("confirmed TUI mode returned no settings write")
	}
	return command
}

// Rust #46883: the saved preference changes only after a successful write and
// the running renderer stays untouched.
func TestTUIModePickerPersistsNextLaunchOnlyLikeRust(t *testing.T) {
	var writes [][]SettingsEdit
	model := tuiModeModel(t, func(edits []SettingsEdit) (SettingsWriteResult, error) {
		writes = append(writes, append([]SettingsEdit(nil), edits...))
		enabled := true
		return SettingsWriteResult{FullscreenTranscript: &enabled}, nil
	}, true)
	if !model.noAltScreen {
		t.Fatal("test setup must start in scrollback mode")
	}
	command := confirmSelectedTUIMode(t, model, 1)
	model.Update(command())
	if len(writes) != 1 || len(writes[0]) != 1 ||
		writes[0][0].KeyPath != "tui.fullscreen_transcript" || writes[0][0].Value != true {
		t.Fatalf("settings writes = %#v", writes)
	}
	if !model.fullscreenTranscript {
		t.Fatal("successful save did not update the stored preference")
	}
	if !model.noAltScreen {
		t.Fatal("saving the next-launch mode changed the running renderer")
	}
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "Saved TUI mode: Fullscreen. Restart Codex to apply; launch overrides still apply.") {
		t.Fatalf("save confirmation missing:\n%s", view)
	}
}

func TestTUIModePickerFailureKeepsStoredPreferenceLikeRust(t *testing.T) {
	model := tuiModeModel(t, func([]SettingsEdit) (SettingsWriteResult, error) {
		return SettingsWriteResult{}, errors.New("boom")
	}, false)
	if !model.fullscreenTranscript {
		t.Fatal("test setup must start with fullscreen preference")
	}
	command := confirmSelectedTUIMode(t, model, 0)
	model.Update(command())
	if !model.fullscreenTranscript {
		t.Fatal("failed save changed the stored preference")
	}
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "Failed to save TUI mode: boom") {
		t.Fatalf("save failure missing:\n%s", view)
	}
}

func TestTUIModePickerCancelWritesNothingLikeRust(t *testing.T) {
	wrote := false
	model := tuiModeModel(t, func([]SettingsEdit) (SettingsWriteResult, error) {
		wrote = true
		return SettingsWriteResult{}, nil
	}, false)
	model.openTUIModePicker()
	model.Update(key(bubbletea.KeyEsc))
	if model.modal != nil || wrote {
		t.Fatalf("cancel left modal=%#v wrote=%v", model.modal, wrote)
	}
}

func TestTUIModePickerMarksStoredPreferenceLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		FullscreenTranscript: boolPtrTea(false),
	})
	model.openTUIModePicker()
	if model.modal == nil || model.modal.id != chatwidget.TUIModePickerViewID {
		t.Fatalf("modal = %#v", model.modal)
	}
	if len(model.modal.options) != 2 ||
		!strings.Contains(model.modal.options[0].Label, "current") ||
		strings.Contains(model.modal.options[1].Label, "current") {
		t.Fatalf("current marker = %#v", model.modal.options)
	}
}
