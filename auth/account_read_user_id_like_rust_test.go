package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAccountReadResponseCarriesChatGPTUserIDLikeRust pins Rust #60004: the
// account snapshot's canonical ChatGPT user id travels with account/read, and
// stays null for API-only and signed-out sessions.
func TestAccountReadResponseCarriesChatGPTUserIDLikeRust(t *testing.T) {
	manager := NewAccountManager()
	manager.ApplyAuthSnapshot(&AuthDotJSON{Tokens: map[string]any{
		"access_token":    "chatgpt-access-token",
		"chatgpt_user_id": "user-123",
		"email":           "dev@example.com",
		"plan_type":       "plus",
	}})

	read := manager.GetAccount(&GetAccountParams{})
	if read.Account == nil || read.Account.Type != AccountChatGPT {
		t.Fatalf("chatgpt account = %#v, want a ChatGPT account", read.Account)
	}
	if read.UserID == nil || *read.UserID != "user-123" {
		t.Fatalf("userId = %#v, want \"user-123\"", read.UserID)
	}
	payload, err := json.Marshal(read)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(payload), `"userId":"user-123"`) {
		t.Fatalf("wire payload = %s, want a userId key", payload)
	}

	// An API-key session has no ChatGPT identity.
	manager.ApplyAuthSnapshot(&AuthDotJSON{OpenAIAPIKey: "sk-test"})
	if read := manager.GetAccount(&GetAccountParams{}); read.UserID != nil {
		t.Fatalf("API-key userId = %#v, want null", read.UserID)
	}

	// A signed-out session reports nothing.
	manager.ApplyAuthSnapshot(nil)
	if read := manager.GetAccount(&GetAccountParams{}); read.UserID != nil {
		t.Fatalf("signed-out userId = %#v, want null", read.UserID)
	}

	// Switching sessions must not carry the previous id across.
	manager.ApplyAuthSnapshot(&AuthDotJSON{Tokens: map[string]any{
		"access_token":    "other-access-token",
		"chatgpt_user_id": "user-456",
		"email":           "other@example.com",
	}})
	if read := manager.GetAccount(&GetAccountParams{}); read.UserID == nil || *read.UserID != "user-456" {
		t.Fatalf("switched userId = %#v, want \"user-456\"", read.UserID)
	}
}
