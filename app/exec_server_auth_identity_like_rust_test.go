package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/execserver"
)

func execServerIdentityTestTokens(account, user, planType, token string) map[string]any {
	return map[string]any{
		"access_token":      token,
		"refresh_token":     "refresh-" + token,
		"account_id":        account,
		"chatgpt_user_id":   user,
		"chatgpt_plan_type": planType,
	}
}

func execServerIdentityTestProvider(t *testing.T, home string) (*execServerRegistryAuth, *auth.StoreOptions) {
	t.Helper()
	options := auth.StoreOptionsFromConfig("file", false)
	expected, err := auth.NewStoreWithOptions(home, options).Load()
	if err != nil || expected == nil {
		t.Fatalf("load expected auth = %#v, %v", expected, err)
	}
	return newExecServerRegistryAuth(home, options, http.DefaultClient, expected), options
}

// Rust #58251: the executor follows a same-owner rotation but rejects an
// account switch instead of sending another owner's credential.
func TestExecServerRegistryAuthPinsAccountLikeRust(t *testing.T) {
	home := t.TempDir()
	saveExecServerChatGPTAuthForTest(t, home, execServerIdentityTestTokens("workspace-a", "user-a", "business", "token-1"))
	live, options := execServerIdentityTestProvider(t, home)

	saveExecServerChatGPTAuthForTest(t, home, execServerIdentityTestTokens("workspace-a", "user-a", "business", "token-2"))
	headers, err := live.ResolveAuthHeaders(context.Background())
	if err != nil {
		t.Fatalf("same-owner rotation rejected: %v", err)
	}
	if got := headers.Get("Authorization"); got != "Bearer token-2" {
		t.Fatalf("Authorization = %q, want the rotated bearer", got)
	}

	if err := auth.NewStoreWithOptions(home, options).Save(auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens:   execServerIdentityTestTokens("workspace-b", "user-a", "business", "token-3"),
	}); err != nil {
		t.Fatalf("switch account: %v", err)
	}
	if _, err := live.ResolveAuthHeaders(context.Background()); !errors.Is(err, errExecServerAuthChanged) {
		t.Fatalf("account switch error = %v, want executor authentication changed", err)
	} else if errors.Is(err, execserver.ErrRegistryCredentialsTransient) {
		t.Fatalf("account switch was classified transient: %v", err)
	}
}

// Rust #58251: the same account with a different user or workspace policy is a
// different executor owner.
func TestExecServerRegistryAuthRejectsChangedUserAndWorkspaceLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		tokens map[string]any
	}{
		{"user", execServerIdentityTestTokens("workspace-a", "user-b", "business", "token-2")},
		{"workspace_policy", execServerIdentityTestTokens("workspace-a", "user-a", "plus", "token-2")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			saveExecServerChatGPTAuthForTest(t, home, execServerIdentityTestTokens("workspace-a", "user-a", "business", "token-1"))
			live, options := execServerIdentityTestProvider(t, home)
			if err := auth.NewStoreWithOptions(home, options).Save(auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: testCase.tokens}); err != nil {
				t.Fatalf("rotate credential: %v", err)
			}
			if _, err := live.ResolveAuthHeaders(context.Background()); !errors.Is(err, errExecServerAuthChanged) {
				t.Fatalf("error = %v, want executor authentication changed", err)
			}
		})
	}
}

// Rust #58251: logout must never keep serving the last known credential.
func TestExecServerRegistryAuthDoesNotReuseCredentialsAfterLogoutLikeRust(t *testing.T) {
	home := t.TempDir()
	saveExecServerChatGPTAuthForTest(t, home, execServerIdentityTestTokens("workspace-a", "user-a", "business", "token-1"))
	live, options := execServerIdentityTestProvider(t, home)
	if _, err := auth.NewStoreWithOptions(home, options).Delete(); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := live.ResolveAuthHeaders(context.Background()); !errors.Is(err, errExecServerRemoteAuthRequired) {
		t.Fatalf("logout error = %v, want remote auth required", err)
	}
}

// Rust #58251/#58355: a token generation whose refresh exchange was cancelled
// stays unavailable until different credentials appear.
func TestExecServerRegistryAuthBlocksUncertainRefreshGenerationLikeRust(t *testing.T) {
	home := t.TempDir()
	saveExecServerChatGPTAuthForTest(t, home, execServerIdentityTestTokens("workspace-a", "user-a", "business", "token-1"))
	refreshServer := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(500 * time.Millisecond):
		}
	}))
	defer refreshServer.Close()
	t.Setenv(auth.RefreshTokenURLEnvOverride, refreshServer.URL)
	live, _ := execServerIdentityTestProvider(t, home)

	rejected, err := live.ResolveAuthHeaders(context.Background())
	if err != nil {
		t.Fatalf("ResolveAuthHeaders() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if live.RefreshAfterUnauthorized(ctx, rejected) {
		t.Fatal("RefreshAfterUnauthorized() = true for a cancelled exchange")
	}
	if _, err := live.ResolveAuthHeaders(context.Background()); !errors.Is(err, execserver.ErrRegistryCredentialsTransient) {
		t.Fatalf("uncertain generation error = %v, want transient credentials", err)
	}
	saveExecServerChatGPTAuthForTest(t, home, execServerIdentityTestTokens("workspace-a", "user-a", "business", "token-2"))
	headers, err := live.ResolveAuthHeaders(context.Background())
	if err != nil {
		t.Fatalf("rotated credential rejected: %v", err)
	}
	if !strings.Contains(headers.Get("Authorization"), "token-2") {
		t.Fatalf("Authorization = %q, want the rotated bearer", headers.Get("Authorization"))
	}
}
