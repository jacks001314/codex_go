package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/auth"
	managedconfig "codex_go/config"
)

// mcpEMAAccountTestIDToken builds the account ID token the enterprise binding
// check reads: the configured issuer, the account OAuth client as audience, and
// the account claim the record must agree with.
func mcpEMAAccountTestIDToken(t *testing.T, issuer string, clientID string, accountID string) string {
	t.Helper()
	return mcpEMATestJWT(t, mcpEMATestJAGHeader(), map[string]any{
		"iss":                issuer,
		"sub":                "account-user",
		"aud":                clientID,
		"exp":                time.Now().Add(time.Hour).Unix(),
		"chatgpt_account_id": accountID,
	})
}

// mcpEMAAccountTestTokens builds one persisted Codex account grant whose
// provenance names the enterprise IdP and an account OAuth client.
func mcpEMAAccountTestTokens(t *testing.T, idp managedconfig.MCPServerIdPOAuthConfig, accountClientID string, loginOwner string) map[string]any {
	t.Helper()
	return map[string]any{
		"oauth_provenance": map[string]any{"issuer": idp.Issuer, "client_id": accountClientID},
		"login_owner_id":   loginOwner,
		"refresh_token":    "account-refresh",
		"access_token":     "account-access",
		"chatgpt_user_id":  "user-1",
		"account_id":       "account-1",
		"id_token":         mcpEMAAccountTestIDToken(t, idp.Issuer, accountClientID, "account-1"),
	}
}

// TestMCPEMAAccountGrantUsabilityLikeRust mirrors #55698: an account grant is
// reusable only with the configured issuer, a complete login, a matching
// account claim, and usable ID-token bindings.
func TestMCPEMAAccountGrantUsabilityLikeRust(t *testing.T) {
	idp := managedconfig.MCPServerIdPOAuthConfig{Issuer: "https://idp.example", ClientID: "account-client"}
	valid := mcpEMAAccountTestTokens(t, idp, "account-client", "login-1")
	if !MCPEMAAccountGrantIsUsable(valid, idp) {
		t.Fatal("valid account grant is not usable")
	}
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing provenance", func(tokens map[string]any) { delete(tokens, "oauth_provenance") }},
		{"other issuer", func(tokens map[string]any) {
			tokens["oauth_provenance"] = map[string]any{"issuer": "https://other.example", "client_id": "account-client"}
		}},
		{"trailing slash issuer", func(tokens map[string]any) {
			tokens["oauth_provenance"] = map[string]any{"issuer": "https://idp.example/", "client_id": "account-client"}
		}},
		{"missing login owner", func(tokens map[string]any) { delete(tokens, "login_owner_id") }},
		{"missing refresh token", func(tokens map[string]any) { delete(tokens, "refresh_token") }},
		{"missing user id", func(tokens map[string]any) { delete(tokens, "chatgpt_user_id") }},
		{"missing account id", func(tokens map[string]any) { delete(tokens, "account_id") }},
		{"id token names another account", func(tokens map[string]any) { tokens["account_id"] = "account-2" }},
		{"id token for another client", func(tokens map[string]any) {
			tokens["id_token"] = mcpEMAAccountTestIDToken(t, idp.Issuer, "other-client", "account-1")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tokens := mcpEMAAccountTestTokens(t, idp, "account-client", "login-1")
			test.mutate(tokens)
			if MCPEMAAccountGrantIsUsable(tokens, idp) {
				t.Fatalf("grant %#v was usable", tokens)
			}
		})
	}
}

// TestMCPEMAAccountConfigurationIssueLikeRust mirrors #55698: a missing login
// owner or a trailing-slash issuer mismatch produces actionable guidance, while
// an unrelated record produces none.
func TestMCPEMAAccountConfigurationIssueLikeRust(t *testing.T) {
	idp := managedconfig.MCPServerIdPOAuthConfig{Issuer: "https://idp.example", ClientID: "account-client"}
	legacy := mcpEMAAccountTestTokens(t, idp, "account-client", "")
	if got := MCPEMAAccountConfigurationIssue(legacy, idp); got != "Sign in to your Codex account again to enable this enterprise MCP server" {
		t.Fatalf("legacy login issue = %q", got)
	}
	slash := mcpEMAAccountTestTokens(t, idp, "account-client", "login-1")
	slash["oauth_provenance"] = map[string]any{"issuer": "https://idp.example/", "client_id": "account-client"}
	if got := MCPEMAAccountConfigurationIssue(slash, idp); !strings.Contains(got, "trailing slash") {
		t.Fatalf("trailing slash issue = %q", got)
	}
	unrelated := mcpEMAAccountTestTokens(t, idp, "account-client", "login-1")
	unrelated["oauth_provenance"] = map[string]any{"issuer": "https://other.example", "client_id": "account-client"}
	if got := MCPEMAAccountConfigurationIssue(unrelated, idp); got != "" {
		t.Fatalf("unrelated record issue = %q", got)
	}
}

type mcpEMAExchangeRecorder struct {
	mu     sync.Mutex
	values []url.Values
	// rejectAccountIDPToken makes the IdP reject the account grant once, so a
	// test can observe the initial-connection fallback to enterprise sign-in.
	rejectAccountIDPToken bool
	// onIDPToken runs before the IdP answers and reports whether this request
	// must be rejected, so a test can rotate the stored grant mid-exchange.
	onIDPToken func(url.Values) bool
}

func (r *mcpEMAExchangeRecorder) record(values url.Values) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values = append(r.values, values)
}

func (r *mcpEMAExchangeRecorder) idpRequests() []url.Values {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]url.Values(nil), r.values...)
}

// mcpEMAAccountTestServer serves the MCP authorization server and the IdP, and
// records the IdP token request form.
func mcpEMAAccountTestServer(t *testing.T, recorder *mcpEMAExchangeRecorder) *httptest.Server {
	t.Helper()
	now := time.Now().Unix()
	server := mcpEMAAuthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server/mcp":
			writeJSON(t, w, map[string]any{
				"authorization_endpoint":                "http://" + r.Host + "/as/authorize",
				"token_endpoint":                        "http://" + r.Host + "/as/token",
				"token_endpoint_auth_methods_supported": []string{"none"},
			})
		case "/.well-known/oauth-authorization-server/idp":
			writeJSON(t, w, map[string]any{
				"issuer":                                "http://" + r.Host + "/idp",
				"authorization_endpoint":                "http://" + r.Host + "/idp/authorize",
				"token_endpoint":                        "http://" + r.Host + "/idp/token",
				"token_endpoint_auth_methods_supported": []string{"none"},
			})
		case "/idp/token":
			if err := r.ParseForm(); err == nil {
				recorder.record(r.PostForm)
			}
			if recorder.onIDPToken != nil && recorder.onIDPToken(r.PostForm) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			if recorder.rejectAccountIDPToken && r.PostForm.Get("client_id") == "account-client" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
				return
			}
			claims := mcpEMATestClaimsAt("http://"+r.Host+"/idp", "http://"+r.Host+"/mcp", "http://"+r.Host+"/mcp", now)
			accessToken, err := mcpEMATestJWTValue(mcpEMATestJAGHeader(), claims)
			if err != nil {
				http.Error(w, "jwt", http.StatusInternalServerError)
				return
			}
			writeJSON(t, w, map[string]any{
				"access_token":      accessToken,
				"issued_token_type": idJAGTokenType,
				"token_type":        "N_A",
				"resource":          claims["resource"],
				"scope":             claims["scope"],
			})
		case "/as/token":
			writeJSON(t, w, mcpEMATestTokenResponse())
		default:
			http.NotFound(w, r)
		}
	})
	return server
}

// TestEMAAuthManagerBorrowsAccountGrantLikeRust mirrors #55698: without a
// stored enterprise sign-in the manager exchanges the Codex account grant,
// authenticating with the account's own OAuth client, and applies the resulting
// bearer to the request.
func TestEMAAuthManagerBorrowsAccountGrantLikeRust(t *testing.T) {
	recorder := &mcpEMAExchangeRecorder{}
	server := mcpEMAAccountTestServer(t, recorder)
	home := t.TempDir()
	profile := managedconfig.MCPEnterpriseManagedAuthConfig{IDP: managedconfig.MCPServerIdPOAuthConfig{
		Issuer: server.URL + "/idp", ClientID: "idp-client",
	}}
	tokens := mcpEMAAccountTestTokens(t, profile.IDP, "account-client", "login-1")
	snapshot := &auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: tokens}
	if err := auth.NewStore(home).Save(*snapshot); err != nil {
		t.Fatal(err)
	}
	manager := newMCPEMAAccountTestManager(t, home, server, profile, func() *auth.AuthDotJSON { return snapshot })
	request, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyHTTPRequest(request, nil); err != nil {
		t.Fatalf("ApplyHTTPRequest() error = %v", err)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer resource-token" {
		t.Fatalf("Authorization = %q", got)
	}
	requests := recorder.idpRequests()
	if len(requests) != 1 {
		t.Fatalf("IdP token requests = %d, want 1", len(requests))
	}
	if got := requests[0].Get("subject_token"); got != "account-refresh" {
		t.Fatalf("IdP subject_token = %q, want the account grant", got)
	}
	if got := requests[0].Get("client_id"); got != "account-client" {
		t.Fatalf("IdP client_id = %q, want the account OAuth client", got)
	}
	if !manager.CredentialsAvailable() {
		t.Fatal("an eligible account grant is a usable credential")
	}
}

// TestEMAAuthManagerRetriesRotatedAccountGrantLikeRust mirrors #55859: when the
// refresh grant rotates under a rejected exchange, the connection retries the
// newer grant instead of failing the call or remembering a stale rejection.
func TestEMAAuthManagerRetriesRotatedAccountGrantLikeRust(t *testing.T) {
	recorder := &mcpEMAExchangeRecorder{}
	server := mcpEMAAccountTestServer(t, recorder)
	home := t.TempDir()
	profile := managedconfig.MCPEnterpriseManagedAuthConfig{IDP: managedconfig.MCPServerIdPOAuthConfig{
		Issuer: server.URL + "/idp", ClientID: "idp-client",
	}}
	original := &auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: mcpEMAAccountTestTokens(t, profile.IDP, "account-client", "login-1")}
	rotatedTokens := mcpEMAAccountTestTokens(t, profile.IDP, "account-client", "login-1")
	rotatedTokens["refresh_token"] = "account-refresh-2"
	rotatedTokens["access_token"] = "account-access-2"
	rotated := &auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: rotatedTokens}
	if err := auth.NewStore(home).Save(*original); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	current := original
	var rotateOnce sync.Once
	recorder.onIDPToken = func(url.Values) bool {
		rejected := false
		rotateOnce.Do(func() {
			rejected = true
			if err := auth.NewStore(home).Save(*rotated); err != nil {
				t.Error(err)
			}
			mu.Lock()
			current = rotated
			mu.Unlock()
		})
		return rejected
	}
	manager := newMCPEMAAccountTestManager(t, home, server, profile, func() *auth.AuthDotJSON {
		mu.Lock()
		defer mu.Unlock()
		return current
	})
	request, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyHTTPRequest(request, nil); err != nil {
		t.Fatalf("ApplyHTTPRequest() error = %v", err)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer resource-token" {
		t.Fatalf("Authorization = %q", got)
	}
	requests := recorder.idpRequests()
	if len(requests) != 2 {
		t.Fatalf("IdP requests = %#v, want the rejected and retried exchanges", requests)
	}
	if requests[0].Get("subject_token") != "account-refresh" || requests[1].Get("subject_token") != "account-refresh-2" {
		t.Fatalf("IdP requests = %#v, want the rotated grant on retry", requests)
	}
	if manager.emaState.RequiresSignIn(manager.registrationIdentity(), rotatedTokens) {
		t.Fatal("a rotated grant must not inherit the previous revision's rejection")
	}
}

// TestEMAAuthManagerPrefersAccountGrantLikeRust mirrors #55859: an eligible
// account grant wins over a saved enterprise sign-in.
func TestEMAAuthManagerPrefersAccountGrantLikeRust(t *testing.T) {
	recorder := &mcpEMAExchangeRecorder{}
	server := mcpEMAAccountTestServer(t, recorder)
	home := t.TempDir()
	profile := managedconfig.MCPEnterpriseManagedAuthConfig{IDP: managedconfig.MCPServerIdPOAuthConfig{
		Issuer: server.URL + "/idp", ClientID: "idp-client",
	}}
	scope := EMAAuthScope{UserID: "user-1", WorkspaceID: "account-1"}
	if err := NewOAuthStoreWithMode(home, OAuthCredentialsStoreFile).Save(&OAuthTokenSet{
		ServerName:   EMAAuthCredentialName(profile.IDP, scope),
		ServerURL:    profile.IDP.Issuer,
		ClientID:     profile.IDP.ClientID,
		RefreshToken: "enterprise-refresh",
		AccessToken:  "idp-access",
	}); err != nil {
		t.Fatal(err)
	}
	tokens := mcpEMAAccountTestTokens(t, profile.IDP, "account-client", "login-1")
	snapshot := &auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: tokens}
	if err := auth.NewStore(home).Save(*snapshot); err != nil {
		t.Fatal(err)
	}
	manager := newMCPEMAAccountTestManager(t, home, server, profile, func() *auth.AuthDotJSON { return snapshot })
	request, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyHTTPRequest(request, nil); err != nil {
		t.Fatalf("ApplyHTTPRequest() error = %v", err)
	}
	requests := recorder.idpRequests()
	if len(requests) != 1 || requests[0].Get("subject_token") != "account-refresh" {
		t.Fatalf("IdP requests = %#v, want the account grant", requests)
	}
	if got := requests[0].Get("client_id"); got != "account-client" {
		t.Fatalf("IdP client_id = %q, want the account OAuth client", got)
	}
}

// TestEMAAuthManagerFallsBackToEnterpriseSignInLikeRust mirrors #55859: when
// the initial connection's account grant is rejected, a saved enterprise
// sign-in completes it and the rejection is remembered for that registration.
func TestEMAAuthManagerFallsBackToEnterpriseSignInLikeRust(t *testing.T) {
	recorder := &mcpEMAExchangeRecorder{rejectAccountIDPToken: true}
	server := mcpEMAAccountTestServer(t, recorder)
	home := t.TempDir()
	profile := managedconfig.MCPEnterpriseManagedAuthConfig{IDP: managedconfig.MCPServerIdPOAuthConfig{
		Issuer: server.URL + "/idp", ClientID: "idp-client",
	}}
	scope := EMAAuthScope{UserID: "user-1", WorkspaceID: "account-1"}
	if err := NewOAuthStoreWithMode(home, OAuthCredentialsStoreFile).Save(&OAuthTokenSet{
		ServerName:   EMAAuthCredentialName(profile.IDP, scope),
		ServerURL:    profile.IDP.Issuer,
		ClientID:     profile.IDP.ClientID,
		RefreshToken: "enterprise-refresh",
		AccessToken:  "idp-access",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := &auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: mcpEMAAccountTestTokens(t, profile.IDP, "account-client", "login-1")}
	if err := auth.NewStore(home).Save(*snapshot); err != nil {
		t.Fatal(err)
	}
	manager := newMCPEMAAccountTestManager(t, home, server, profile, func() *auth.AuthDotJSON { return snapshot })
	request, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyHTTPRequest(request, nil); err != nil {
		t.Fatalf("ApplyHTTPRequest() error = %v", err)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer resource-token" {
		t.Fatalf("Authorization = %q", got)
	}
	requests := recorder.idpRequests()
	if len(requests) != 2 {
		t.Fatalf("IdP requests = %#v, want the rejected account exchange and the enterprise sign-in", requests)
	}
	if requests[0].Get("subject_token") != "account-refresh" || requests[1].Get("subject_token") != "enterprise-refresh" {
		t.Fatalf("IdP requests = %#v, want account then enterprise credentials", requests)
	}
	if got := requests[1].Get("client_id"); got != "idp-client" {
		t.Fatalf("fallback IdP client_id = %q, want the configured IdP client", got)
	}
	if !manager.emaState.RequiresSignIn(manager.registrationIdentity(), snapshot.Tokens) {
		t.Fatal("rejected account grant was not remembered for this registration")
	}
	if !manager.CredentialsAvailable() {
		t.Fatal("the saved enterprise sign-in is still a usable credential")
	}
}

// TestEMAAuthManagerRejectsStaleAccountGrantLikeRust mirrors #55698: a pinned
// connection rechecks the grant before every exchange, so a replacement for a
// different OAuth client fails instead of being exchanged.
func TestEMAAuthManagerRejectsStaleAccountGrantLikeRust(t *testing.T) {
	recorder := &mcpEMAExchangeRecorder{}
	server := mcpEMAAccountTestServer(t, recorder)
	home := t.TempDir()
	profile := managedconfig.MCPEnterpriseManagedAuthConfig{IDP: managedconfig.MCPServerIdPOAuthConfig{
		Issuer: server.URL + "/idp", ClientID: "idp-client",
	}}
	original := &auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: mcpEMAAccountTestTokens(t, profile.IDP, "account-client", "login-1")}
	if err := auth.NewStore(home).Save(*original); err != nil {
		t.Fatal(err)
	}
	current := original
	manager := newMCPEMAAccountTestManager(t, home, server, profile, func() *auth.AuthDotJSON { return current })
	source, err := manager.pinCredentialSource(context.Background())
	if err != nil || source != emaCredentialAccount || manager.accountClientID != "account-client" {
		t.Fatalf("pinCredentialSource() = %v, error = %v", source, err)
	}
	replacement := &auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: mcpEMAAccountTestTokens(t, profile.IDP, "other-client", "login-2")}
	if err := auth.NewStore(home).Save(*replacement); err != nil {
		t.Fatal(err)
	}
	current = replacement
	request, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = manager.ApplyHTTPRequest(request, nil)
	var tokenSourceErr *EMATokenSourceError
	if !errors.As(err, &tokenSourceErr) || !tokenSourceErr.IsAuthenticationRequired() {
		t.Fatalf("ApplyHTTPRequest() error = %v, want account reauthentication", err)
	}
	if got := tokenSourceErr.Error(); got != "Sign in to your Codex account to restore enterprise MCP access" {
		t.Fatalf("guidance = %q", got)
	}
	if len(recorder.idpRequests()) != 0 {
		t.Fatal("a stale account grant reached the IdP")
	}
}

// TestEMAAuthManagerExplainsMissingAccountGrantLikeRust mirrors #55698: a
// missing enterprise sign-in and an unusable account grant report an actionable
// sign-in or issuer configuration message.
func TestEMAAuthManagerExplainsMissingAccountGrantLikeRust(t *testing.T) {
	profile := managedconfig.MCPEnterpriseManagedAuthConfig{IDP: managedconfig.MCPServerIdPOAuthConfig{
		Issuer: "https://idp.example", ClientID: "idp-client",
	}}
	legacy := mcpEMAAccountTestTokens(t, profile.IDP, "account-client", "")
	slash := mcpEMAAccountTestTokens(t, profile.IDP, "account-client", "login-1")
	slash["oauth_provenance"] = map[string]any{"issuer": "https://idp.example/", "client_id": "account-client"}
	for _, test := range []struct {
		name   string
		tokens map[string]any
		want   string
	}{
		{"legacy login", legacy, "Sign in to your Codex account again to enable this enterprise MCP server"},
		{"trailing slash", slash, "Codex sign-in and enterprise MCP issuer strings differ by a trailing slash; align their OIDC issuer configuration"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			snapshot := &auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: test.tokens}
			if err := auth.NewStore(home).Save(*snapshot); err != nil {
				t.Fatal(err)
			}
			manager, err := NewEMAAuthManager("enterprise", &ServerConfig{
				URL: "https://resource.example/mcp", Auth: ServerAuthEMAAuth, OAuthClientID: "mcp-client",
			}, EMAAuthOptions{
				CodexHome:           home,
				StoreMode:           OAuthCredentialsStoreFile,
				Profile:             profile,
				Scope:               EMAAuthScope{UserID: "user-1", WorkspaceID: "account-1"},
				AccountGrantOptions: mcpEMAAccountTestGrantOptions(home, func() *auth.AuthDotJSON { return snapshot }),
			})
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequest(http.MethodPost, "https://resource.example/mcp", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.ApplyHTTPRequest(request, nil); err == nil || err.Error() != test.want {
				t.Fatalf("ApplyHTTPRequest() error = %v, want %q", err, test.want)
			}
		})
	}
}

func mcpEMAAccountTestGrantOptions(home string, cached func() *auth.AuthDotJSON) *auth.AccountOAuthGrantLeaseOptions {
	return &auth.AccountOAuthGrantLeaseOptions{
		CodexHome:    home,
		StoreOptions: auth.StoreOptionsFromConfig("", false),
		Cached:       cached,
	}
}

func newMCPEMAAccountTestManager(t *testing.T, home string, server *httptest.Server, profile managedconfig.MCPEnterpriseManagedAuthConfig, cached func() *auth.AuthDotJSON) *EMAAuthManager {
	t.Helper()
	manager, err := NewEMAAuthManager("enterprise", &ServerConfig{
		URL:           server.URL + "/mcp",
		Auth:          ServerAuthEMAAuth,
		OAuthClientID: "mcp-client",
		OAuthResource: server.URL + "/mcp",
		Scopes:        []string{"files.read"},
	}, EMAAuthOptions{
		CodexHome:           home,
		StoreMode:           OAuthCredentialsStoreFile,
		Profile:             profile,
		Scope:               EMAAuthScope{UserID: "user-1", WorkspaceID: "account-1"},
		AccountGrantOptions: mcpEMAAccountTestGrantOptions(home, cached),
		HTTPClient:          server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
