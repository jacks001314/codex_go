package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// TestModelPickerUsesActiveThreadProviderLikeRust mirrors Rust #63553: the
// picker asks for the active thread's provider catalog, not the app-server
// default, and a new thread without an explicit provider keeps the default.
func TestModelPickerUsesActiveThreadProviderLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.Model = "acme-model"
	state.Provider = "acme"
	var gotProvider string
	m := NewModel(state, Options{
		OnListModels: func(cwd string, provider string, includeHidden bool) ([]codextui.ModelPickerOption, error) {
			gotProvider = provider
			return []codextui.ModelPickerOption{{ID: "acme-model", Label: "Acme"}}, nil
		},
	})
	cmd := m.fetchModelsForPicker()
	if cmd == nil {
		t.Fatal("fetchModelsForPicker did not return a command")
	}
	msg := cmd()
	if gotProvider != "acme" {
		t.Fatalf("picker provider = %q, want acme", gotProvider)
	}
	if _, ok := msg.(ModelsResultMsg); !ok {
		t.Fatalf("fetch result = %#v, want ModelsResultMsg", msg)
	}

	state.Provider = ""
	cmd = m.fetchModelsForPicker()
	if cmd == nil {
		t.Fatal("fetchModelsForPicker without a provider did not return a command")
	}
	_ = cmd()
	if gotProvider != "" {
		t.Fatalf("default picker provider = %q, want empty", gotProvider)
	}
}
