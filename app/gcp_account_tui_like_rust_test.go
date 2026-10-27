package app

import (
	"testing"

	"codex_go/auth"
	"codex_go/config"
)

// Rust #56265: the TUI treats `Account::Gcp` exactly like
// `Account::AmazonBedrock` and a missing account. It produces no status account
// display, no workspace row, no ChatGPT email refresh, and no OpenAI login
// requirement. The app-server half is covered by appserver/gcp_test.go
// (account/read reports {"type":"gcp"} with requiresOpenaiAuth=false).
func TestGCPAccountTUIStatusLikeRust(t *testing.T) {
	gcp := &auth.Account{Type: auth.AccountGcp}

	if display, chatgpt := interactiveAccountDisplay(gcp); display != "" || chatgpt {
		t.Fatalf("account display = %q chatgpt=%v, want no ChatGPT identity", display, chatgpt)
	}
	if got := interactiveAccountWorkspaceDisplay(gcp); got != "" {
		t.Fatalf("workspace display = %q, want empty", got)
	}

	response := &auth.GetAccountResponse{Account: gcp}
	display, chatgpt, requiresOpenAIAuth := remoteStatusAccountValues(response)
	if display != "" || chatgpt {
		t.Fatalf("remote status display = %q chatgpt=%v, want no ChatGPT identity", display, chatgpt)
	}
	if requiresOpenAIAuth == nil || *requiresOpenAIAuth {
		t.Fatalf("requiresOpenAIAuth = %v, want false", requiresOpenAIAuth)
	}
	if got := remoteStatusWorkspaceValue(response); got != "" {
		t.Fatalf("remote workspace = %q, want empty", got)
	}

	// Rust's account bootstrap carries no auth mode for Gcp, and a stray "gcp"
	// spelling must clear the row instead of being treated as ChatGPT.
	plan := auth.PlanPro
	for _, mode := range []string{"", "gcp"} {
		if status, hasChatGPT := remoteAccountStatusFromAuthMode(mode, &plan); status != nil || hasChatGPT {
			t.Fatalf("auth mode %q status = %#v chatgpt=%v, want cleared", mode, status, hasChatGPT)
		}
	}
}

// The local launch derives `requires_openai_auth` from the provider. The
// built-in GCP provider never asks for an OpenAI login, matching Rust's
// `requires_openai_auth = false` bootstrap for Account::Gcp.
func TestGCPProviderDoesNotRequireOpenAIAuthLikeRust(t *testing.T) {
	requires := interactiveRequiresOpenAIAuth(&config.Config{Values: map[string]any{"model_provider": "gcp"}})
	if requires == nil || *requires {
		t.Fatalf("requiresOpenAIAuth = %v, want false", requires)
	}
}
