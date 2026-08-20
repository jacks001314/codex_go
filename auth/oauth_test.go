package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildAuthorizeURL(t *testing.T) {
	url, err := BuildAuthorizeURL(&OAuthOptions{
		Issuer:           "https://auth.example.test",
		ClientID:         "client-test",
		ForcedWorkspaces: []string{" workspace-a ", "", "workspace-b"},
	}, "http://localhost:1455/auth/callback", &PKCECodes{
		CodeVerifier:  "verifier",
		CodeChallenge: "challenge",
	}, "state-test")
	if err != nil {
		t.Fatalf("BuildAuthorizeURL() error = %v", err)
	}
	for _, want := range []string{
		"response_type=code",
		"client_id=client-test",
		"code_challenge=challenge",
		"state=state-test",
		"allowed_workspace_id=workspace-a%2Cworkspace-b",
	} {
		if !strings.Contains(url, want) {
			t.Fatalf("authorize URL missing %q: %s", want, url)
		}
	}
}

func TestPersistChatGPTTokens(t *testing.T) {
	home := t.TempDir()
	jwt := fakeJWT(map[string]any{"chatgpt_account_id": "account-123"})
	err := PersistChatGPTTokens(home, &ExchangedTokens{
		IDToken:      jwt,
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
	})
	if err != nil {
		t.Fatalf("PersistChatGPTTokens() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var stored AuthDotJSON
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if stored.AuthMode != "chatgpt" || stored.Tokens["account_id"] != "account-123" || stored.Tokens["access_token"] != "access-token" {
		t.Fatalf("stored auth = %#v", stored)
	}
}

func TestLogoutWithRevokeRevokesRefreshTokenThenRemovesAuth(t *testing.T) {
	home := t.TempDir()
	jwt := fakeJWT(map[string]any{"chatgpt_account_id": "account-123"})
	if err := PersistChatGPTTokens(home, &ExchangedTokens{
		IDToken:      jwt,
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
	}); err != nil {
		t.Fatalf("PersistChatGPTTokens() error = %v", err)
	}
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/revoke" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		writeJSONAuth(t, w, map[string]string{"message": "success"})
	}))
	defer server.Close()
	t.Setenv(RevokeTokenURLEnvOverride, server.URL+"/oauth/revoke")
	t.Setenv(LoginClientIDEnvOverride, "staging-client")

	removed, err := LogoutWithRevoke(context.Background(), home, nil)
	if err != nil {
		t.Fatalf("LogoutWithRevoke() error = %v", err)
	}
	if !removed {
		t.Fatal("LogoutWithRevoke removed = false, want true")
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth.json should be removed: %v", err)
	}
	if requestBody["token"] != "refresh-token" || requestBody["token_type_hint"] != "refresh_token" || requestBody["client_id"] != "staging-client" {
		t.Fatalf("revoke request body = %#v", requestBody)
	}
}

func TestLogoutWithRevokeIgnoresAccessTokenEnv(t *testing.T) {
	home := t.TempDir()
	jwt := fakeJWT(map[string]any{"chatgpt_account_id": "account-123"})
	if err := PersistChatGPTTokens(home, &ExchangedTokens{
		IDToken:      jwt,
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
	}); err != nil {
		t.Fatalf("PersistChatGPTTokens() error = %v", err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		writeJSONAuth(t, w, map[string]string{"message": "success"})
	}))
	defer server.Close()
	t.Setenv(RevokeTokenURLEnvOverride, server.URL+"/oauth/revoke")
	t.Setenv(CodexAccessTokenEnv, "at-env-token")

	removed, err := LogoutWithRevoke(context.Background(), home, nil)
	if err != nil {
		t.Fatalf("LogoutWithRevoke() error = %v", err)
	}
	if !removed {
		t.Fatal("LogoutWithRevoke removed = false, want true")
	}
	if requests != 1 {
		t.Fatalf("revoke requests = %d, want 1", requests)
	}
}

func TestLogoutAllStoresRemovesEphemeralAndManagedStores(t *testing.T) {
	home := t.TempDir()
	if err := NewStore(home).Save(FromAPIKey("sk-file")); err != nil {
		t.Fatalf("Save file auth returned error: %v", err)
	}
	ephemeralOptions := &StoreOptions{Mode: AuthCredentialsStoreEphemeral}
	if err := NewStoreWithOptions(home, ephemeralOptions).Save(FromChatGPTAuthTokens("access-token", "account-123", nil)); err != nil {
		t.Fatalf("Save ephemeral auth returned error: %v", err)
	}
	removed, err := LogoutAllStores(home, nil)
	if err != nil {
		t.Fatalf("LogoutAllStores() error = %v", err)
	}
	if !removed {
		t.Fatal("LogoutAllStores removed = false, want true")
	}
	if loaded, err := NewStore(home).Load(); err != nil || loaded != nil {
		t.Fatalf("file store after logout = %+v, err = %v", loaded, err)
	}
	if loaded, err := NewStoreWithOptions(home, ephemeralOptions).Load(); err != nil || loaded != nil {
		t.Fatalf("ephemeral store after logout = %+v, err = %v", loaded, err)
	}
}

func TestPersistChatGPTTokensReadsNestedChatGPTClaims(t *testing.T) {
	home := t.TempDir()
	jwt := fakeJWT(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id":          "account-nested",
			"email":                       "nested@example.com",
			"chatgpt_plan_type":           "pro",
			"chatgpt_account_is_fedramp":  true,
			"ignored_for_direct_fallback": "value",
		},
		"chatgpt_account_id": "account-top",
	})
	claims := ChatGPTClaimsFromJWT(jwt)
	if claims.AccountID != "account-nested" || claims.Email != "nested@example.com" || claims.PlanType != "pro" || !claims.FedRAMP {
		t.Fatalf("claims = %+v", claims)
	}
	if err := PersistChatGPTTokens(home, &ExchangedTokens{
		IDToken:      jwt,
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
	}); err != nil {
		t.Fatalf("PersistChatGPTTokens() error = %v", err)
	}
	loaded, err := NewStore(home).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Tokens["account_id"] != "account-nested" {
		t.Fatalf("persisted account_id = %#v", loaded.Tokens["account_id"])
	}
}

func TestRefreshChatGPTTokensPersistsUpdatedAccessToken(t *testing.T) {
	clearAuthEnvForRefreshTests(t)
	home := t.TempDir()
	jwt := fakeJWT(map[string]any{"chatgpt_account_id": "account-123"})
	if err := PersistChatGPTTokens(home, &ExchangedTokens{
		IDToken:      jwt,
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
	}); err != nil {
		t.Fatalf("PersistChatGPTTokens() error = %v", err)
	}
	var requestBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		writeJSONAuth(t, w, map[string]string{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
		})
	}))
	defer server.Close()

	refreshed, err := RefreshChatGPTTokens(context.Background(), &RefreshChatGPTTokenOptions{
		CodexHome: home,
		Issuer:    server.URL,
		ClientID:  "client-test",
	})
	if err != nil {
		t.Fatalf("RefreshChatGPTTokens() error = %v", err)
	}
	if requestBody["grant_type"] != "refresh_token" || requestBody["refresh_token"] != "old-refresh-token" || requestBody["client_id"] != "client-test" {
		t.Fatalf("request body = %#v", requestBody)
	}
	if refreshed.Tokens["access_token"] != "new-access-token" || refreshed.Tokens["refresh_token"] != "new-refresh-token" || refreshed.Tokens["account_id"] != "account-123" {
		t.Fatalf("refreshed auth = %#v", refreshed)
	}
}

func TestRefreshChatGPTTokensRecordsPermanentFailure(t *testing.T) {
	clearAuthEnvForRefreshTests(t)
	home := t.TempDir()
	if err := PersistChatGPTTokens(home, &ExchangedTokens{
		IDToken:      fakeJWT(map[string]any{"chatgpt_account_id": "account-123"}),
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
	}); err != nil {
		t.Fatalf("PersistChatGPTTokens() error = %v", err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusUnauthorized)
		writeJSONAuth(t, w, map[string]any{
			"error": map[string]any{"code": "refresh_token_reused"},
		})
	}))
	defer server.Close()

	_, err := RefreshChatGPTTokens(context.Background(), &RefreshChatGPTTokenOptions{
		CodexHome: home,
		Issuer:    server.URL,
		ClientID:  "client-test",
	})
	if got := RefreshTokenFailedReasonFromError(err); got == nil || *got != RefreshTokenFailedExhausted {
		t.Fatalf("first refresh error = %v, reason = %+v", err, got)
	}
	loaded, loadErr := NewStore(home).Load()
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if failed := RefreshFailureForAuth(home, loaded); failed == nil || failed.Reason != RefreshTokenFailedExhausted {
		t.Fatalf("recorded failure = %+v", failed)
	}

	_, err = RefreshChatGPTTokens(context.Background(), &RefreshChatGPTTokenOptions{
		CodexHome: home,
		Issuer:    server.URL,
		ClientID:  "client-test",
	})
	if got := RefreshTokenFailedReasonFromError(err); got == nil || *got != RefreshTokenFailedExhausted {
		t.Fatalf("second refresh error = %v, reason = %+v", err, got)
	}
	if requests != 1 {
		t.Fatalf("refresh endpoint requests = %d, want 1", requests)
	}
}

func TestRefreshChatGPTTokensInvalidGrantIsPermanentLikeRust(t *testing.T) {
	clearAuthEnvForRefreshTests(t)
	home := t.TempDir()
	if err := PersistChatGPTTokens(home, &ExchangedTokens{
		IDToken:      fakeJWT(map[string]any{"chatgpt_account_id": "account-123"}),
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
	}); err != nil {
		t.Fatalf("PersistChatGPTTokens() error = %v", err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadRequest)
		writeJSONAuth(t, w, map[string]any{"error": "invalid_grant"})
	}))
	defer server.Close()

	_, err := RefreshChatGPTTokens(context.Background(), &RefreshChatGPTTokenOptions{
		CodexHome: home,
		Issuer:    server.URL,
		ClientID:  "client-test",
	})
	if got := RefreshTokenFailedReasonFromError(err); got == nil || *got != RefreshTokenFailedOther {
		t.Fatalf("first refresh error = %v, reason = %+v; want permanent Other", err, got)
	}
	loaded, loadErr := NewStore(home).Load()
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if failed := RefreshFailureForAuth(home, loaded); failed == nil || failed.Reason != RefreshTokenFailedOther {
		t.Fatalf("recorded failure = %+v, want permanent Other", failed)
	}

	_, err = RefreshChatGPTTokens(context.Background(), &RefreshChatGPTTokenOptions{
		CodexHome: home,
		Issuer:    server.URL,
		ClientID:  "client-test",
	})
	if got := RefreshTokenFailedReasonFromError(err); got == nil || *got != RefreshTokenFailedOther {
		t.Fatalf("second refresh error = %v, reason = %+v", err, got)
	}
	if requests != 1 {
		t.Fatalf("refresh endpoint requests = %d, want 1 (failure cached)", requests)
	}
}

func TestRefreshChatGPTTokensOtherBadRequestStaysTransientLikeRust(t *testing.T) {
	clearAuthEnvForRefreshTests(t)
	home := t.TempDir()
	if err := PersistChatGPTTokens(home, &ExchangedTokens{
		IDToken:      fakeJWT(map[string]any{"chatgpt_account_id": "account-123"}),
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
	}); err != nil {
		t.Fatalf("PersistChatGPTTokens() error = %v", err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadRequest)
		writeJSONAuth(t, w, map[string]any{"error": "invalid_request"})
	}))
	defer server.Close()

	for attempt := 0; attempt < 2; attempt++ {
		_, err := RefreshChatGPTTokens(context.Background(), &RefreshChatGPTTokenOptions{
			CodexHome: home,
			Issuer:    server.URL,
			ClientID:  "client-test",
		})
		if IsPermanentRefreshFailure(err) {
			t.Fatalf("attempt %d: invalid_request must stay transient, got permanent %v", attempt+1, err)
		}
		if err == nil {
			t.Fatalf("attempt %d: refresh should fail", attempt+1)
		}
	}
	if requests != 2 {
		t.Fatalf("refresh endpoint requests = %d, want 2 (retryable)", requests)
	}
}

func TestRefreshTokenFailureClassifiesBackendCodes(t *testing.T) {
	cases := []struct {
		code string
		want RefreshTokenFailedReason
	}{
		{code: "refresh_token_expired", want: RefreshTokenFailedExpired},
		{code: "refresh_token_reused", want: RefreshTokenFailedExhausted},
		{code: "refresh_token_invalidated", want: RefreshTokenFailedRevoked},
		{code: "unexpected", want: RefreshTokenFailedOther},
	}
	for _, tc := range cases {
		failed := classifyRefreshTokenFailure(`{"error":{"code":"` + tc.code + `"}}`)
		if failed.Reason != tc.want {
			t.Fatalf("classify %q = %s, want %s", tc.code, failed.Reason, tc.want)
		}
	}
}

func TestExtractRefreshTokenErrorCodeShapes(t *testing.T) {
	for body, want := range map[string]string{
		`{"error":{"code":"refresh_token_expired"}}`: "refresh_token_expired",
		`{"error":"invalid_grant"}`:                  "invalid_grant",
		`{"code":"top_level_code"}`:                  "top_level_code",
		`not json`:                                   "",
		``:                                           "",
	} {
		if got := extractRefreshTokenErrorCode(body); got != want {
			t.Fatalf("extractRefreshTokenErrorCode(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestRunDeviceCodeLogin(t *testing.T) {
	home := t.TempDir()
	jwt := fakeJWT(map[string]any{"chatgpt_account_id": "account-456"})
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSONAuth(t, w, map[string]string{
				"device_auth_id": "device-1",
				"user_code":      "CODE-123",
				"interval":       "0",
			})
		case "/api/accounts/deviceauth/token":
			polls++
			if polls == 1 {
				http.Error(w, "pending", http.StatusForbidden)
				return
			}
			writeJSONAuth(t, w, map[string]string{
				"authorization_code": "auth-code",
				"code_challenge":     "challenge",
				"code_verifier":      "verifier",
			})
		case "/oauth/token":
			writeJSONAuth(t, w, map[string]string{
				"id_token":      jwt,
				"access_token":  "access-token-123",
				"refresh_token": "refresh-token-123",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var prompt strings.Builder
	err := RunDeviceCodeLogin(context.Background(), &OAuthOptions{
		CodexHome:    home,
		Issuer:       server.URL,
		ClientID:     "client-test",
		PollInterval: time.Millisecond,
		PollTimeout:  time.Second,
		DevicePrompt: &prompt,
	})
	if err != nil {
		t.Fatalf("RunDeviceCodeLogin() error = %v", err)
	}
	if !strings.Contains(prompt.String(), "CODE-123") {
		t.Fatalf("prompt = %q", prompt.String())
	}
	loaded, err := NewStore(home).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.Tokens["refresh_token"] != "refresh-token-123" || loaded.Tokens["account_id"] != "account-456" {
		t.Fatalf("loaded auth = %#v", loaded)
	}
}

func TestRunDeviceCodeLoginRejectsWorkspaceMismatch(t *testing.T) {
	home := t.TempDir()
	jwt := fakeJWT(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "workspace-denied",
		},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSONAuth(t, w, map[string]string{
				"device_auth_id": "device-1",
				"user_code":      "CODE-123",
				"interval":       "1",
			})
		case "/api/accounts/deviceauth/token":
			writeJSONAuth(t, w, map[string]string{
				"authorization_code": "auth-code",
				"code_challenge":     "challenge",
				"code_verifier":      "verifier",
			})
		case "/oauth/token":
			writeJSONAuth(t, w, map[string]string{
				"id_token":      jwt,
				"access_token":  "access-token-123",
				"refresh_token": "refresh-token-123",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := RunDeviceCodeLogin(context.Background(), &OAuthOptions{
		CodexHome:        home,
		Issuer:           server.URL,
		ClientID:         "client-test",
		PollInterval:     time.Millisecond,
		PollTimeout:      time.Second,
		ForcedWorkspaces: []string{"workspace-allowed"},
	})
	if err == nil || !strings.Contains(err.Error(), "Login is restricted to workspace id(s) workspace-allowed.") {
		t.Fatalf("RunDeviceCodeLogin() error = %v", err)
	}
	loaded, loadErr := NewStore(home).Load()
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if loaded != nil {
		t.Fatalf("auth should not be persisted on workspace mismatch: %+v", loaded)
	}
}

func TestStartBrowserLoginRedirectsToSuccessPage(t *testing.T) {
	home := t.TempDir()
	jwt := fakeJWT(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "account-browser",
		},
	})
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			writeJSONAuth(t, w, map[string]string{
				"id_token":      jwt,
				"access_token":  "access-browser",
				"refresh_token": "refresh-browser",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()

	server, err := StartBrowserLogin(context.Background(), &OAuthOptions{
		CodexHome:    home,
		Issuer:       issuer.URL,
		ClientID:     "client-test",
		OpenBrowser:  false,
		CallbackPort: freeTCPPortOAuthTest(t),
		ForceState:   "state-browser",
		StoreOptions: nil,
		HTTPClient:   issuer.Client(),
		PollInterval: time.Millisecond,
		PollTimeout:  time.Second,
		DevicePrompt: io.Discard,
	})
	if err != nil {
		t.Fatalf("StartBrowserLogin returned error: %v", err)
	}
	callback := "http://127.0.0.1:" + portStringOAuthTest(server.Port) + "/auth/callback?code=auth-code&state=state-browser"
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(callback)
	if err != nil {
		t.Fatalf("callback GET returned error: %v", err)
	}
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "/success?") {
		t.Fatalf("callback status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()
	successResp, err := issuer.Client().Get(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("success GET returned error: %v", err)
	}
	successBody, _ := io.ReadAll(successResp.Body)
	_ = successResp.Body.Close()
	if !strings.Contains(string(successBody), "Successfully logged in") {
		t.Fatalf("success body = %q", string(successBody))
	}
	if err := <-server.Done; err != nil {
		t.Fatalf("server Done error = %v", err)
	}
	loaded, err := NewStore(home).Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if loaded == nil || loaded.Tokens["refresh_token"] != "refresh-browser" || loaded.Tokens["account_id"] != "account-browser" {
		t.Fatalf("loaded auth = %+v", loaded)
	}
}

func TestStartBrowserLoginOAuthErrorPageDoesNotPersist(t *testing.T) {
	home := t.TempDir()
	server, err := StartBrowserLogin(context.Background(), &OAuthOptions{
		CodexHome:    home,
		Issuer:       "https://auth.example.test",
		ClientID:     "client-test",
		OpenBrowser:  false,
		CallbackPort: freeTCPPortOAuthTest(t),
		ForceState:   "state-denied",
	})
	if err != nil {
		t.Fatalf("StartBrowserLogin returned error: %v", err)
	}
	resp, err := http.Get("http://127.0.0.1:" + portStringOAuthTest(server.Port) + "/auth/callback?state=state-denied&error=access_denied&error_description=missing_codex_entitlement")
	if err != nil {
		t.Fatalf("callback GET returned error: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "Codex is not enabled for your workspace") {
		t.Fatalf("status=%d body=%q", resp.StatusCode, string(body))
	}
	if err := <-server.Done; err == nil || !strings.Contains(err.Error(), "Codex is not enabled") {
		t.Fatalf("Done error = %v", err)
	}
	loaded, err := NewStore(home).Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if loaded != nil {
		t.Fatalf("auth should not be persisted: %+v", loaded)
	}
}

func TestStartBrowserLoginCancelsPreviousServerOnSamePort(t *testing.T) {
	port := freeTCPPortOAuthTest(t)
	first, err := StartBrowserLogin(context.Background(), &OAuthOptions{
		CodexHome:    t.TempDir(),
		Issuer:       "https://auth.example.test",
		ClientID:     "client-test",
		OpenBrowser:  false,
		CallbackPort: port,
		ForceState:   "first-state",
	})
	if err != nil {
		t.Fatalf("first StartBrowserLogin returned error: %v", err)
	}
	second, err := StartBrowserLogin(context.Background(), &OAuthOptions{
		CodexHome:    t.TempDir(),
		Issuer:       "https://auth.example.test",
		ClientID:     "client-test",
		OpenBrowser:  false,
		CallbackPort: port,
		ForceState:   "second-state",
	})
	if err != nil {
		t.Fatalf("second StartBrowserLogin returned error: %v", err)
	}
	if second.Port != first.Port {
		t.Fatalf("second port = %d, first port = %d", second.Port, first.Port)
	}
	select {
	case err := <-first.Done:
		if err == nil || !strings.Contains(err.Error(), "Login cancelled") {
			t.Fatalf("first Done error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first server did not shut down after second server canceled it")
	}
	resp, err := http.Get("http://127.0.0.1:" + portStringOAuthTest(second.Port) + "/cancel")
	if err != nil {
		t.Fatalf("cancel GET returned error: %v", err)
	}
	_ = resp.Body.Close()
	select {
	case <-second.Done:
	case <-time.After(3 * time.Second):
		t.Fatal("second server did not shut down after cancel")
	}
}

func writeJSONAuth(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}

func freeTCPPortOAuthTest(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer listener.Close()
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

func portStringOAuthTest(port uint16) string {
	return strings.TrimSpace(fmt.Sprintf("%d", port))
}

func fakeJWT(payload map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "none"})
	body, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

func clearAuthEnvForRefreshTests(t *testing.T) {
	t.Helper()
	t.Setenv(OpenAIAPIKeyEnv, "")
	t.Setenv(CodexAPIKeyEnv, "")
	t.Setenv(CodexAccessTokenEnv, "")
}
