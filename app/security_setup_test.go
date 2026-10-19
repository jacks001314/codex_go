package app

import (
	"testing"

	"codex_go/auth"
)

func TestSecuritySetupIdentityRequiresAccountUserAndToken(t *testing.T) {
	snapshot := auth.FromChatGPTAuthTokens("token", "account", nil)
	snapshot.Tokens["chatgpt_user_id"] = "user"
	identity, token, ok := securitySetupIdentity(&snapshot)
	if !ok || identity != "account|user" || token != "token" {
		t.Fatalf("identity = %q token = %q ok = %v", identity, token, ok)
	}

	missingUser := snapshot
	delete(missingUser.Tokens, "chatgpt_user_id")
	if _, _, ok := securitySetupIdentity(&missingUser); ok {
		t.Fatal("identity resolved without a ChatGPT user id")
	}
	missingToken := snapshot
	delete(missingToken.Tokens, "access_token")
	if _, _, ok := securitySetupIdentity(&missingToken); ok {
		t.Fatal("identity resolved without an access token")
	}
	if _, _, ok := securitySetupIdentity(nil); ok {
		t.Fatal("nil auth resolved an identity")
	}
}
