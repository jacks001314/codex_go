package tea

import (
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// TestModelPickerKeepsDefaultCatalogWhenSwitchingProvidersLikeRust mirrors
// Rust #63553's per-provider ModelCatalog: refreshing an alternate provider's
// catalog keeps the default (new-thread) catalog available, and a response for
// another provider never replaces the active thread's list.
func TestModelPickerKeepsDefaultCatalogWhenSwitchingProvidersLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	m := NewModel(state, Options{Width: 100, Height: 30})

	m.pendingModelsRequestID = 1
	m.applyModelsResult(ModelsResultMsg{
		RequestID: 1,
		Provider:  "",
		Options:   []codextui.ModelPickerOption{{ID: "default-model", Label: "Default"}},
	})

	state.Provider = "acme"
	m.pendingModelsRequestID = 2
	m.applyModelsResult(ModelsResultMsg{
		RequestID: 2,
		Provider:  "acme",
		Options:   []codextui.ModelPickerOption{{ID: "acme-model", Label: "Acme"}},
	})
	if len(m.modelPickerOpts) != 1 || m.modelPickerOpts[0].ID != "acme-model" {
		t.Fatalf("active catalog = %#v, want acme", m.modelPickerOpts)
	}
	if got := m.modelPickerOptionsFor(""); len(got) != 1 || got[0].ID != "default-model" {
		t.Fatalf("default catalog = %#v, want default-model", got)
	}

	// A late response for a different provider stays cached without touching
	// the active thread's list.
	m.pendingModelsRequestID = 3
	m.applyModelsResult(ModelsResultMsg{
		RequestID: 3,
		Provider:  "other",
		Options:   []codextui.ModelPickerOption{{ID: "other-model", Label: "Other"}},
	})
	if m.modelPickerOpts[0].ID != "acme-model" {
		t.Fatalf("active catalog changed by another provider: %#v", m.modelPickerOpts)
	}
	if got := m.modelPickerOptionsFor("other"); len(got) != 1 || got[0].ID != "other-model" {
		t.Fatalf("other catalog = %#v", got)
	}

	// A thread switch to a cached provider selects that provider's catalog.
	state.Provider = ""
	m.applyThreadSettingsValues(appserver.Settings{ModelProvider: "acme"})
	if len(m.modelPickerOpts) != 1 || m.modelPickerOpts[0].ID != "acme-model" {
		t.Fatalf("selected catalog = %#v, want acme", m.modelPickerOpts)
	}

	// Returning to the default provider opens the preserved default catalog.
	state.Provider = ""
	_ = m.openModelPicker()
	if m.modal == nil || m.modal.modelPicker == nil || len(m.modal.modelPicker.Options) != 1 ||
		m.modal.modelPicker.Options[0].ID != "default-model" {
		t.Fatalf("default picker = %#v", m.modal)
	}
}
