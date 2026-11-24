package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// TestAlternateProviderModelSelectionIsSessionOnlyLikeRust mirrors Rust #63553:
// a model chosen while the thread runs on a provider other than the configured
// default applies to the current session only, while a default-provider choice
// still persists the user default.
func TestAlternateProviderModelSelectionIsSessionOnlyLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.Provider = "acme"
	m := NewModel(state, Options{DefaultModelProvider: "openai"})
	if !m.modelSelectionIsSessionOnly() {
		t.Fatal("alternate provider selection was not session-only")
	}
	state.Provider = "openai"
	if m.modelSelectionIsSessionOnly() {
		t.Fatal("default provider selection was marked session-only")
	}
	state.Provider = ""
	if m.modelSelectionIsSessionOnly() {
		t.Fatal("an unknown provider was marked session-only")
	}

	sessionOnly, ok := ResolveModelSelection(&PickerDecision{
		Kind:            "model",
		Value:           "acme-model",
		ReasoningEffort: "high",
		SessionOnly:     true,
	})
	if !ok || sessionOnly.ThreadModel == nil || *sessionOnly.ThreadModel != "acme-model" || len(sessionOnly.Persist) != 0 {
		t.Fatalf("session-only plan = %#v", sessionOnly)
	}

	persistent, ok := ResolveModelSelection(&PickerDecision{
		Kind:            "model",
		Value:           "acme-model",
		ReasoningEffort: "high",
	})
	if !ok || len(persistent.Persist) != 1 || persistent.Persist[0].Label != modelSelectionPersistLabel {
		t.Fatalf("persistent plan = %#v", persistent)
	}
}

// TestSessionOnlyFooterDropsTheSecondaryHintLikeRust mirrors Rust #63553
// session_model_shortcut: when the choice is session-only, accept already
// means "session" and the redundant "s session" hint disappears.
func TestSessionOnlyFooterDropsTheSecondaryHintLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.Provider = "acme"
	m := NewModel(state, Options{DefaultModelProvider: "openai"})
	hint := m.modelSessionFooterHint("high")
	if !strings.Contains(hint, "enter session") {
		t.Fatalf("session-only hint = %q, want enter session", hint)
	}
	if strings.Contains(hint, modelSessionSecondaryKey+" session") {
		t.Fatalf("session-only hint kept the secondary shortcut: %q", hint)
	}

	state.Provider = "openai"
	defaultHint := m.modelSessionFooterHint("high")
	if !strings.Contains(defaultHint, "enter default") || !strings.Contains(defaultHint, modelSessionSecondaryKey+" session") {
		t.Fatalf("default provider hint = %q", defaultHint)
	}
}

// TestUnscopedCatalogForcesSessionOnlyLikeRust mirrors Rust #63553: a
// non-Cloud server that cannot scope the catalog still allows model changes,
// but they stay on the current session.
func TestUnscopedCatalogForcesSessionOnlyLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	state.Provider = "openai"
	m := NewModel(state, Options{DefaultModelProvider: "openai"})
	m.pendingModelsRequestID = 1
	m.applyModelsResult(ModelsResultMsg{
		RequestID:   1,
		Provider:    "openai",
		Options:     []codextui.ModelPickerOption{{ID: "gpt-a"}},
		Scoped:      false,
		ScopedKnown: true,
	})
	if !m.modelSelectionIsSessionOnly() {
		t.Fatal("an unscoped non-Cloud catalog was not session-only")
	}

	m.pendingModelsRequestID = 2
	m.applyModelsResult(ModelsResultMsg{
		RequestID:   2,
		Provider:    "openai",
		Options:     []codextui.ModelPickerOption{{ID: "gpt-a"}},
		Scoped:      true,
		ScopedKnown: true,
	})
	if m.modelSelectionIsSessionOnly() {
		t.Fatal("a scoped default catalog stayed session-only")
	}

	m.serverOwnedSettings = true
	m.pendingModelsRequestID = 3
	m.applyModelsResult(ModelsResultMsg{
		RequestID:   3,
		Provider:    "openai",
		Options:     []codextui.ModelPickerOption{{ID: "gpt-a"}},
		Scoped:      false,
		ScopedKnown: true,
	})
	if m.modelSelectionIsSessionOnly() {
		t.Fatal("Cloud was marked session-only by an unscoped catalog")
	}
}

// TestAccountChangeRefetchesNonCloudCatalogLikeRust mirrors Rust #63553
// settings.rs: an account change on a non-Cloud server whose catalogs were
// just cleared refreshes them without waiting for /model.
func TestAccountChangeRefetchesNonCloudCatalogLikeRust(t *testing.T) {
	loader := func(cwd string, provider string, includeHidden bool) ([]codextui.ModelPickerOption, error) {
		return []codextui.ModelPickerOption{{ID: "gpt-a"}}, nil
	}
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	state.AccountDisplay = "old@example.com"
	m := NewModel(state, Options{OnListModels: loader})
	_ = m.applyAccountStatusUpdated(AccountStatusUpdatedMsg{Display: "new@example.com"})
	if m.pendingModelsRequestID == 0 {
		t.Fatal("non-Cloud account change did not refetch the catalog")
	}

	cloudState := codextui.NewState(nil)
	cloudState.SetThreadID("thread-b")
	cloudState.AccountDisplay = "old@example.com"
	cloud := NewModel(cloudState, Options{OnListModels: loader, ServerOwnedSettings: true})
	_ = cloud.applyAccountStatusUpdated(AccountStatusUpdatedMsg{Display: "new@example.com"})
	if cloud.pendingModelsRequestID != 0 {
		t.Fatal("Cloud account change refetched a provider catalog")
	}
}
