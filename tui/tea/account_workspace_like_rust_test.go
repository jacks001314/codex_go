package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// TestAccountStatusWorkspaceLikeRust covers the account-update and async
// identity paths replacing the /status workspace value (Rust #57366).
func TestAccountStatusWorkspaceLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	model.Update(AccountStatusUpdatedMsg{
		Display:           "dev@example.com (business)",
		Workspace:         "Acme (ws-1)",
		HasChatGPTAccount: true,
		EmailRequestID:    "req-1",
	})
	if got := model.State.WorkspaceDisplay; got != "Acme (ws-1)" {
		t.Fatalf("WorkspaceDisplay = %q, want %q", got, "Acme (ws-1)")
	}
	model.Update(AccountEmailLoadedMsg{RequestID: "req-1", Email: "dev@example.com", Workspace: "Acme (ws-2)"})
	if got := model.State.WorkspaceDisplay; got != "Acme (ws-2)" {
		t.Fatalf("WorkspaceDisplay after email load = %q, want %q", got, "Acme (ws-2)")
	}
}
