package tea

import (
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// TestUncachedProviderDropsStaleCatalogLikeRust mirrors Rust #63553: switching
// the active thread to a provider with no cached catalog dismisses the picker
// built from the previous provider and asks for the new provider's list before
// any picker surface offers models again.
func TestUncachedProviderDropsStaleCatalogLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	var requested []string
	m := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnListModels: func(cwd string, provider string, includeHidden bool) ([]codextui.ModelPickerOption, error) {
			requested = append(requested, provider)
			return []codextui.ModelPickerOption{{ID: "zeta-model", Label: "Zeta"}}, nil
		},
	})
	m.pendingModelsRequestID = 1
	m.applyModelsResult(ModelsResultMsg{
		RequestID: 1,
		Provider:  "acme",
		Options:   []codextui.ModelPickerOption{{ID: "acme-model", Label: "Acme"}},
	})
	state.Provider = "acme"
	_ = m.openModelPicker()
	if m.modal == nil || m.modal.modelPicker == nil {
		t.Fatalf("model picker did not open: %#v", m.modal)
	}

	m.applyThreadSettingsValues(appserver.Settings{ModelProvider: "zeta"})
	if m.modal != nil {
		t.Fatalf("stale picker stayed open: %#v", m.modal)
	}
	if len(m.modelPickerOpts) != 0 {
		t.Fatalf("uncached provider kept the previous catalog: %#v", m.modelPickerOpts)
	}
	if got := m.modelPickerOptionsFor("zeta"); len(got) != 0 {
		t.Fatalf("uncached provider offered models: %#v", got)
	}

	cmd := m.consumeModelCatalogRefresh()
	if cmd == nil {
		t.Fatal("provider switch did not request a catalog refresh")
	}
	if m.modelCatalogRefreshPending {
		t.Fatal("refresh flag survived the fetch")
	}
	msg, ok := cmd().(ModelsResultMsg)
	if !ok {
		t.Fatalf("refresh command returned %T", cmd())
	}
	if msg.Provider != "zeta" {
		t.Fatalf("refreshed provider = %q, want zeta", msg.Provider)
	}
	if len(requested) != 1 || requested[0] != "zeta" {
		t.Fatalf("requested providers = %#v, want [zeta]", requested)
	}
	m.applyModelsResult(msg)
	if len(m.modelPickerOpts) != 1 || m.modelPickerOpts[0].ID != "zeta-model" {
		t.Fatalf("refreshed catalog = %#v, want zeta-model", m.modelPickerOpts)
	}
}

// TestThreadOnConfiguredDefaultKeepsStartupCatalogLikeRust mirrors Rust
// #63553: a new thread whose provider is the server's default reads the same
// catalog the TUI started with, so attaching to it does not discard the list
// or fetch a duplicate one.
func TestThreadOnConfiguredDefaultKeepsStartupCatalogLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	m := NewModel(state, Options{Width: 100, Height: 30, DefaultModelProvider: "openai"})
	m.pendingModelsRequestID = 1
	m.applyModelsResult(ModelsResultMsg{
		RequestID: 1,
		Provider:  "",
		Options:   []codextui.ModelPickerOption{{ID: "default-model", Label: "Default"}},
	})

	m.applyThreadSettingsValues(appserver.Settings{ModelProvider: "openai"})
	if len(m.modelPickerOpts) != 1 || m.modelPickerOpts[0].ID != "default-model" {
		t.Fatalf("startup catalog = %#v, want default-model", m.modelPickerOpts)
	}
	if m.modelCatalogRefreshPending {
		t.Fatal("default provider requested a redundant catalog refresh")
	}
	if got := m.modelPickerOptionsFor("openai"); len(got) != 1 || got[0].ID != "default-model" {
		t.Fatalf("default provider catalog = %#v", got)
	}
}

// TestAPIKeyProviderSwitchClearsAccessProgramsLikeRust mirrors Rust #63553
// refresh_account_scoped_models: an API-key account scopes cyber access
// metadata to the thread and cwd, so switching providers drops the cached
// programs and reads the catalog again.
func TestAPIKeyProviderSwitchClearsAccessProgramsLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		account     string
		wantProgram bool
		wantRefresh bool
	}{
		{name: "api key", account: "API key configured", wantProgram: false, wantRefresh: true},
		{name: "chatgpt", account: "user@example.com", wantProgram: true, wantRefresh: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			state := codextui.NewState(nil)
			state.SetThreadID("thread-a")
			state.AccountDisplay = testCase.account
			m := NewModel(state, Options{Width: 100, Height: 30})
			m.pendingModelsRequestID = 1
			m.applyModelsResult(ModelsResultMsg{
				RequestID: 1,
				Provider:  "acme",
				Options:   []codextui.ModelPickerOption{{ID: "acme-model", Label: "Acme", AvailableCyberPrograms: []string{"standard"}}},
			})
			m.applyThreadSettingsValues(appserver.Settings{ModelProvider: "acme"})
			if len(m.modelPickerOpts) != 1 || m.modelPickerOpts[0].ID != "acme-model" {
				t.Fatalf("selected catalog = %#v", m.modelPickerOpts)
			}
			hasProgram := m.modelPickerOpts[0].AvailableCyberPrograms != nil
			if hasProgram != testCase.wantProgram {
				t.Fatalf("access programs present = %v, want %v", hasProgram, testCase.wantProgram)
			}
			if m.modelCatalogRefreshPending != testCase.wantRefresh {
				t.Fatalf("refresh pending = %v, want %v", m.modelCatalogRefreshPending, testCase.wantRefresh)
			}
			if testCase.wantProgram {
				cached := m.modelPickerOptsByProvider["acme"]
				if len(cached) != 1 || cached[0].AvailableCyberPrograms == nil {
					t.Fatalf("cached programs were cleared: %#v", cached)
				}
			}
		})
	}
}
