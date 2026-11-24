package tea

import (
	"errors"
	"testing"

	"codex_go/config"
	codextui "codex_go/tui"
)

// TestProviderSaveBlocksModelPickerLikeRust mirrors Rust #63553
// model_picker_unavailable: while the default-provider save holds
// provider_write, neither the picker nor a named model may be resolved.
func TestProviderSaveBlocksModelPickerLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	m := NewModel(state, Options{Width: 100, Height: 30})
	m.modelProviderWritePending = true
	const want = "Saving the default provider. Try /model after the save finishes."

	if cmd := m.openModelPicker(); cmd != nil || m.modal != nil {
		t.Fatalf("picker opened while a provider save was running: %#v", m.modal)
	}
	if m.notice != want {
		t.Fatalf("picker notice = %q, want %q", m.notice, want)
	}
	m.notice = ""
	if cmd := m.applyModelSetting("gpt-5"); cmd != nil {
		t.Fatal("named model applied while a provider save was running")
	}
	if m.notice != want {
		t.Fatalf("named model notice = %q, want %q", m.notice, want)
	}
}

// TestProviderSwitchMarksUntaggedCatalogUnsafeLikeRust mirrors Rust #63553:
// after a confirmed switch the untagged catalog is never applied again, and
// /model reports the stale list.
func TestProviderSwitchMarksUntaggedCatalogUnsafeLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	m := NewModel(state, Options{Width: 100, Height: 30})
	m.modelProviderWriteChanged = true

	m.pendingModelsRequestID = 1
	m.applyModelsResult(ModelsResultMsg{
		RequestID: 1,
		Provider:  "",
		Options:   []codextui.ModelPickerOption{{ID: "stale-model", Label: "Stale"}},
	})
	if len(m.modelPickerOpts) != 0 {
		t.Fatalf("applied an untagged catalog after the switch: %#v", m.modelPickerOpts)
	}
	if len(m.modelPickerOptsByProvider) != 0 {
		t.Fatalf("cached an untagged catalog after the switch: %#v", m.modelPickerOptsByProvider)
	}

	state.Provider = "acme"
	m.pendingModelsRequestID = 2
	m.applyModelsResult(ModelsResultMsg{
		RequestID: 2,
		Provider:  "acme",
		Options:   []codextui.ModelPickerOption{{ID: "acme-model", Label: "Acme"}},
	})
	if len(m.modelPickerOpts) != 1 || m.modelPickerOpts[0].ID != "acme-model" {
		t.Fatalf("provider-scoped catalog = %#v", m.modelPickerOpts)
	}

	const want = "The model list may be out of date. Restart the app server and TUI to refresh /model."
	m.notice = ""
	if cmd := m.openModelPicker(); cmd != nil || m.modal != nil {
		t.Fatalf("picker opened with an unsafe catalog: %#v", m.modal)
	}
	if m.notice != want {
		t.Fatalf("unsafe catalog notice = %q, want %q", m.notice, want)
	}
}

// TestProviderSaveResultTransitionsLikeRust mirrors Rust #63553 write.rs: the
// pending lock clears on every result, a confirmed switch (or an unconfirmed
// transport failure) marks the catalog unsafe, and an overridden or failed
// save leaves it usable.
func TestProviderSaveResultTransitionsLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		response    config.ConfigWriteResponse
		err         error
		wantChanged bool
	}{
		{name: "saved", response: config.ConfigWriteResponse{Status: config.WriteOK}, wantChanged: true},
		{name: "writer fallback", response: config.ConfigWriteResponse{}, wantChanged: true},
		{name: "overridden", response: config.ConfigWriteResponse{Status: config.WriteOKOverridden}, wantChanged: false},
		{name: "unconfirmed", err: ErrModelProviderDefaultUnconfirmed, wantChanged: true},
		{name: "failed", err: errors.New("server rejected the write"), wantChanged: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			state := codextui.NewState(nil)
			m := NewModel(state, Options{Width: 100, Height: 30})
			m.modelProviderWritePending = true
			m.applyConfigPanelProviderWriteResult(configPanelProviderWriteResultMsg{
				kind:     "default",
				response: testCase.response,
				err:      testCase.err,
			})
			if m.modelProviderWritePending {
				t.Fatal("pending provider save was not released")
			}
			if m.modelProviderWriteChanged != testCase.wantChanged {
				t.Fatalf("changed = %v, want %v", m.modelProviderWriteChanged, testCase.wantChanged)
			}
		})
	}
}
