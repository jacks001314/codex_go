package tea

import (
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
