package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// TestAccountEmailRefreshOnlyAppliesCurrentRequestLikeRust mirrors #50199:
// account updates replace the display immediately, while asynchronous email
// results are accepted only for the newest request.
func TestAccountEmailRefreshOnlyAppliesCurrentRequestLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		AccountDisplay:    "old@example.com (Plus)",
		HasChatGPTAccount: true,
	})
	model := NewModel(state, Options{Width: 100, Height: 30})

	model.Update(AccountStatusUpdatedMsg{
		Display:           "Pro 200",
		HasChatGPTAccount: true,
		EmailRequestID:    "current-request",
	})
	if got := model.State.AccountDisplay; got != "Pro 200" {
		t.Fatalf("account after update = %q, want Pro 200", got)
	}
	if !model.hasChatGPTAccount || !model.State.HasChatGPTAccount {
		t.Fatal("ChatGPT account capability was not retained")
	}

	model.Update(AccountEmailLoadedMsg{RequestID: "stale-request", Email: "stale@example.com"})
	if got := model.State.AccountDisplay; got != "Pro 200" {
		t.Fatalf("stale email changed account = %q, want Pro 200", got)
	}

	model.Update(AccountEmailLoadedMsg{RequestID: "current-request", Email: "user@example.com"})
	if got := model.State.AccountDisplay; got != "user@example.com (Pro 200)" {
		t.Fatalf("current email account = %q, want user@example.com (Pro 200)", got)
	}
	if model.accountEmailRequestID != "" {
		t.Fatalf("request id = %q, want cleared after delivery", model.accountEmailRequestID)
	}
	model.composer.SetValue("/status")
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if view := model.View(); !strings.Contains(view, "Account:") || !strings.Contains(view, "user@example.com (Pro 200)") {
		t.Fatalf("/status missing refreshed account identity:\n%s", view)
	}
}

// TestAccountStatusUpdateReplacesAPIKeyModeLikeRust checks that switching away
// from ChatGPT disables ChatGPT-only status and usage surfaces.
func TestAccountStatusUpdateReplacesAPIKeyModeLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		AccountDisplay:    "user@example.com (Pro 200)",
		HasChatGPTAccount: true,
	})
	model := NewModel(state, Options{Width: 100, Height: 30, HasChatGPTAccount: true})

	model.Update(AccountStatusUpdatedMsg{
		Display:           "API key configured (run codex login to use ChatGPT)",
		HasChatGPTAccount: false,
	})

	if got := model.State.AccountDisplay; got != "API key configured (run codex login to use ChatGPT)" {
		t.Fatalf("account display = %q", got)
	}
	if model.hasChatGPTAccount || model.State.HasChatGPTAccount {
		t.Fatal("API key update must clear ChatGPT account capability")
	}
	if model.accountEmailRequestID != "" {
		t.Fatalf("request id = %q, want empty", model.accountEmailRequestID)
	}
}
