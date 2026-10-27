package app

import (
	"testing"

	"codex_go/auth"
)

// TestInteractiveAccountWorkspaceDisplayLikeRust covers the local /status
// workspace row (Rust #57366 workspace_display): the trimmed account name is
// shown for ChatGPT accounts only, and an invalid name yields no row.
func TestInteractiveAccountWorkspaceDisplayLikeRust(t *testing.T) {
	name := "  Example workspace  "
	invalid := "bad\x00name"
	tests := []struct {
		name    string
		account *auth.Account
		want    string
	}{
		{name: "chatgpt workspace", account: &auth.Account{Type: auth.AccountChatGPT, WorkspaceName: &name}, want: "Example workspace"},
		{name: "control characters discarded", account: &auth.Account{Type: auth.AccountChatGPT, WorkspaceName: &invalid}, want: ""},
		{name: "missing name", account: &auth.Account{Type: auth.AccountChatGPT}, want: ""},
		{name: "api key account", account: &auth.Account{Type: auth.AccountAPIKey, WorkspaceName: &name}, want: ""},
		{name: "no account", account: nil, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := interactiveAccountWorkspaceDisplay(test.account); got != test.want {
				t.Fatalf("interactiveAccountWorkspaceDisplay() = %q, want %q", got, test.want)
			}
		})
	}
}
