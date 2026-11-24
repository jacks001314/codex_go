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
	m.markModelProviderWritePending(m.backendID)
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
	m.finishModelProviderWrite(m.backendID, true)

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

// TestProviderWriteGuardIsPerBackendLikeRust mirrors Rust #63553
// provider_writes being keyed by BackendId: a save or switch on one backend
// leaves another backend's /model usable.
func TestProviderWriteGuardIsPerBackendLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	m := NewModel(state, Options{Width: 100, Height: 30})
	backendA := codextui.NewBackendID()
	backendB := codextui.NewBackendID()
	m.backendID = backendA
	m.markModelProviderWritePending(backendA)
	m.finishModelProviderWrite(backendA, true)

	m.backendID = backendB
	if message := m.modelPickerUnavailable(); message != "" {
		t.Fatalf("backend B inherited backend A's guard: %q", message)
	}
	if cmd := m.openModelPicker(); m.modal == nil {
		t.Fatalf("backend B picker did not open (cmd nil=%v)", cmd == nil)
	}

	m.backendID = backendA
	if message := m.modelPickerUnavailable(); message == "" {
		t.Fatal("backend A lost its guard")
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
			backend := m.backendID
			m.markModelProviderWritePending(backend)
			m.applyConfigPanelProviderWriteResult(configPanelProviderWriteResultMsg{
				backend:  backend,
				kind:     "default",
				response: testCase.response,
				err:      testCase.err,
			})
			if m.modelProviderWritePending[backend] {
				t.Fatal("pending provider save was not released")
			}
			if got := m.modelProviderWriteChanged[backend]; got != testCase.wantChanged {
				t.Fatalf("changed = %v, want %v", got, testCase.wantChanged)
			}
		})
	}
}
