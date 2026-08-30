package tea

import (
	"testing"

	codextui "codex_go/tui"
)

func TestModelPickerAsyncFetchLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	m := NewModel(state, Options{
		OnListModels: func(includeHidden bool) ([]codextui.ModelPickerOption, error) {
			return []codextui.ModelPickerOption{{ID: "gpt-fresh", Label: "Fresh", IsDefault: true}}, nil
		},
	})
	cmd := m.openModelPicker()
	if cmd == nil {
		t.Fatal("openModelPicker did not return a fetch command")
	}
	msg := cmd()
	result, ok := msg.(ModelsResultMsg)
	if !ok || result.Err != nil || len(result.Options) != 1 || result.Options[0].ID != "gpt-fresh" {
		t.Fatalf("fetch result = %#v", msg)
	}
	updated, _ := m.Update(result)
	m = updated.(*Model)
	if len(m.modelPickerOpts) != 1 || m.modelPickerOpts[0].ID != "gpt-fresh" {
		t.Fatalf("modelPickerOpts = %#v", m.modelPickerOpts)
	}
	if m.modal == nil || m.modal.modelPicker == nil || len(m.modal.modelPicker.Options) != 1 || m.modal.modelPicker.Options[0].ID != "gpt-fresh" {
		t.Fatalf("modal model picker = %#v", m.modal)
	}
	if m.pendingModelsRequestID != 0 {
		t.Fatalf("pendingModelsRequestID = %d, want reset to 0", m.pendingModelsRequestID)
	}
}
