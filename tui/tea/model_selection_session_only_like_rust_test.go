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
