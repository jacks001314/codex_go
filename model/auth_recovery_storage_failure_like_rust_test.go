package model

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/auth"
)

// Rust #58324: an unreadable or malformed auth.json must surface as a
// transient reload failure instead of the permanent account-mismatch error, so
// the cached credential and unauthorized recovery survive an interrupted write.
func TestReloadStoredChatGPTAuthTreatsStorageFailureAsTransientLikeRust(t *testing.T) {
	home := t.TempDir()
	cached := &auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token":  "cached-access-token",
			"refresh_token": "cached-refresh-token",
			"account_id":    "account-123",
		},
	}
	runner := &ResponsesAgentRunner{
		CodexHome:    home,
		AuthSnapshot: cached,
		StoreOptions: &auth.StoreOptions{Mode: auth.AuthCredentialsStoreFile},
	}
	for name, contents := range map[string]string{
		"empty file":                 "",
		"invalid outer field":        `{"auth_mode":"private-credential-value"}`,
		"invalid credential payload": `{"agent_identity": "header.private-credential-value.signature"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(contents), 0o600); err != nil {
				t.Fatalf("write auth.json: %v", err)
			}
			_, err := runner.reloadStoredChatGPTAuth(context.Background())
			if err == nil {
				t.Fatal("reload accepted a broken auth.json")
			}
			if auth.IsPermanentRefreshFailure(err) {
				t.Fatalf("storage failure was classified permanent: %v", err)
			}
			message := err.Error()
			if strings.Contains(message, "private-credential-value") || strings.Contains(message, home) {
				t.Fatalf("reload error leaked credentials or path: %q", message)
			}
			if runner.AuthSnapshot != cached || runner.AuthSnapshot.Tokens["access_token"] != "cached-access-token" {
				t.Fatalf("cached credential was replaced: %#v", runner.AuthSnapshot)
			}
		})
	}
}

// The same failure keeps the recovery plan on its reload step, so a later 401
// retries after the file is restored (Rust #58324 "unauthorized recovery can
// resume after the file is restored").
func TestUnauthorizedRecoveryRetriesReloadAfterStorageFailureLikeRust(t *testing.T) {
	home := t.TempDir()
	cached := &auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token":  "cached-access-token",
			"refresh_token": "cached-refresh-token",
			"account_id":    "account-123",
		},
	}
	runner := &ResponsesAgentRunner{
		CodexHome:    home,
		AuthSnapshot: cached,
		StoreOptions: &auth.StoreOptions{Mode: auth.AuthCredentialsStoreFile},
	}
	authPath := filepath.Join(home, "auth.json")
	if err := os.WriteFile(authPath, []byte("{"), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	state := &authRecoveryState{}
	if _, _, err := runner.recoverUnauthorizedAuth(context.Background(), state, authRecoveryDebug{}); err == nil {
		t.Fatal("recovery accepted a broken auth.json")
	}
	if state.planIndex != 0 {
		t.Fatalf("transient reload advanced the recovery plan: %d", state.planIndex)
	}
	if err := os.WriteFile(authPath, []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"new-access-token","refresh_token":"new-refresh-token","account_id":"account-123"}}`), 0o600); err != nil {
		t.Fatalf("restore auth.json: %v", err)
	}
	if _, _, err := runner.recoverUnauthorizedAuth(context.Background(), state, authRecoveryDebug{}); err != nil {
		t.Fatalf("recovery did not retry the reload: %v", err)
	}
	if runner.AuthSnapshot.Tokens["access_token"] != "new-access-token" {
		t.Fatalf("recovery did not adopt the restored credential: %#v", runner.AuthSnapshot)
	}
}
