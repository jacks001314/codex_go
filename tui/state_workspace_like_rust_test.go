package tui

import (
	"strings"
	"testing"
)

// TestStatusCardShowsChatGPTWorkspaceLikeRust mirrors Rust #57366: the
// workspace row sits directly after the account row and is dropped when a
// connected server's provider is not the built-in one.
func TestStatusCardShowsChatGPTWorkspaceLikeRust(t *testing.T) {
	state := NewState(&Options{
		Provider:         "openai",
		AccountDisplay:   "dev@example.com (business)",
		WorkspaceDisplay: "Acme (ws-1)",
	})
	state.SetThreadID("thread-1")
	card := state.RenderStatusCard()
	accountIndex := strings.Index(card, "Account:")
	workspaceIndex := strings.Index(card, "ChatGPT workspace:")
	if workspaceIndex < 0 || workspaceIndex < accountIndex {
		t.Fatalf("status card missing workspace row after the account:\n%s", card)
	}
	if !strings.Contains(card, "Acme (ws-1)") {
		t.Fatalf("status card missing workspace value:\n%s", card)
	}

	// A connected server owns its provider definitions, so a custom provider
	// cannot prove the credentials belong to the workspace.
	state.RemoteConnection = true
	state.Provider = "custom"
	if card := state.RenderStatusCard(); strings.Contains(card, "ChatGPT workspace:") {
		t.Fatalf("custom remote provider kept the workspace row:\n%s", card)
	}

	// The built-in provider id stays eligible, as does an unknown provider.
	state.Provider = "openai"
	if card := state.RenderStatusCard(); !strings.Contains(card, "ChatGPT workspace:") {
		t.Fatalf("built-in remote provider dropped the workspace row:\n%s", card)
	}
	state.Provider = ""
	if card := state.RenderStatusCard(); !strings.Contains(card, "ChatGPT workspace:") {
		t.Fatalf("unknown remote provider dropped the workspace row:\n%s", card)
	}
}
