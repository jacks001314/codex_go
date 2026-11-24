package tea

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// TestModelPickerShowsLoadingUntilCatalogArrivesLikeRust mirrors Rust #63553
// model_popups: an uncached provider opens a loading view, and the fetched
// list replaces it in place.
func TestModelPickerShowsLoadingUntilCatalogArrivesLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	state.Provider = "acme"
	m := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnListModels: func(cwd string, provider string, includeHidden bool) ([]codextui.ModelPickerOption, error) {
			return []codextui.ModelPickerOption{{ID: "acme-model", Label: "Acme"}}, nil
		},
	})

	cmd := m.openModelPicker()
	if m.modal == nil {
		t.Fatal("loading view did not open")
	}
	if !m.modal.modelPickerLoading || m.modal.modelPicker != nil {
		t.Fatalf("loading view state = %#v", m.modal)
	}
	if len(m.modal.options) != 1 || !strings.Contains(m.modal.options[0].Label, "Loading models") || !m.modal.options[0].Disabled {
		t.Fatalf("loading options = %#v", m.modal.options)
	}
	if cmd == nil {
		t.Fatal("loading view did not start a catalog fetch")
	}

	updated, _ := m.Update(cmd())
	m = updated.(*Model)
	if m.modal == nil {
		t.Fatal("fetched catalog closed the picker")
	}
	if m.modal.modelPickerLoading {
		t.Fatal("loading flag survived the fetched catalog")
	}
	if m.modal.modelPicker == nil || len(m.modal.modelPicker.Options) != 1 || m.modal.modelPicker.Options[0].ID != "acme-model" {
		t.Fatalf("fetched picker = %#v", m.modal.modelPicker)
	}
	if len(m.modal.options) != 1 || strings.Contains(m.modal.options[0].Label, "Loading models") {
		t.Fatalf("modal options were not replaced: %#v", m.modal.options)
	}
}

// TestModelPickerLoadFailureClosesTheLoadingViewLikeRust mirrors Rust
// #63553 on_models_loaded: a failed fetch reports an error instead of leaving
// the loading view spinning.
func TestModelPickerLoadFailureClosesTheLoadingViewLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	state.Provider = "acme"
	m := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnListModels: func(cwd string, provider string, includeHidden bool) ([]codextui.ModelPickerOption, error) {
			return nil, errors.New("catalog unavailable")
		},
	})
	cmd := m.openModelPicker()
	if cmd == nil {
		t.Fatal("loading view did not start a catalog fetch")
	}
	updated, _ := m.Update(cmd())
	m = updated.(*Model)
	if m.modal != nil {
		t.Fatalf("failed load kept a modal open: %#v", m.modal)
	}
	if m.notice != "Could not load models. Try /model again." {
		t.Fatalf("failure notice = %q", m.notice)
	}
}

// TestModelPickerKeepsBundledCatalogWithoutCallbackLikeRust mirrors the
// fallback used by runtimes that cannot list models.
func TestModelPickerKeepsBundledCatalogWithoutCallbackLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	m := NewModel(state, Options{Width: 100, Height: 30})
	if cmd := m.openModelPicker(); cmd != nil {
		t.Fatal("bundled fallback unexpectedly fetched a catalog")
	}
	if m.modal == nil || m.modal.modelPicker == nil || len(m.modal.modelPicker.Options) == 0 {
		t.Fatalf("bundled picker did not open: %#v", m.modal)
	}
}
