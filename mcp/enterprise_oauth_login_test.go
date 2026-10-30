package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// enterpriseTestIdP serves RMCP-shaped discovery metadata and a token endpoint.
func enterpriseTestIdP(t *testing.T, mutate func(metadata map[string]any), tokenResponse func() map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case mcpOAuthAuthorizationServerWellKnownPath:
			metadata := map[string]any{
				"issuer":                                "http://" + r.Host,
				"authorization_endpoint":                "http://" + r.Host + "/authorize",
				"token_endpoint":                        "http://" + r.Host + "/token",
				"token_endpoint_auth_methods_supported": []string{"none"},
			}
			if mutate != nil {
				mutate(metadata)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(metadata)
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(tokenResponse())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// TestResolveEnterpriseAuthorizationMetadataStrictness covers Rust #43844's
// discovery validation: published metadata bound to the configured issuer,
// validated endpoints, and an advertised public-client auth method.
func TestResolveEnterpriseAuthorizationMetadataStrictness(t *testing.T) {
	ctx := context.Background()
	server := enterpriseTestIdP(t, nil, func() map[string]any { return map[string]any{} })
	metadata, err := resolveEnterpriseAuthorizationMetadata(ctx, server.URL, "client", "", "", nil)
	if err != nil {
		t.Fatalf("resolve metadata: %v", err)
	}
	if metadata.Issuer != server.URL || metadata.AuthorizationEndpoint != server.URL+"/authorize" || metadata.TokenEndpoint != server.URL+"/token" {
		t.Fatalf("metadata = %#v", metadata)
	}

	// A metadata issuer that differs only by a root trailing slash passes the
	// discovery binding but fails the exact enterprise comparison.
	slashOnly := enterpriseTestIdP(t, func(metadata map[string]any) {
		metadata["issuer"] = metadata["issuer"].(string) + "/"
	}, func() map[string]any { return map[string]any{} })
	if _, err := resolveEnterpriseAuthorizationMetadata(ctx, slashOnly.URL, "client", "", "", nil); err == nil ||
		!strings.Contains(err.Error(), "does not match configuration") {
		t.Fatalf("root-slash issuer error = %v", err)
	}

	// A mismatched published issuer aborts discovery.
	mismatched := enterpriseTestIdP(t, func(metadata map[string]any) {
		metadata["issuer"] = "https://other.example.com"
	}, func() map[string]any { return map[string]any{} })
	if _, err := resolveEnterpriseAuthorizationMetadata(ctx, mismatched.URL, "client", "", "", nil); err == nil {
		t.Fatal("a mismatched issuer must be rejected")
	}

	// Public-client token endpoint authentication must be advertised.
	noPublicAuth := enterpriseTestIdP(t, func(metadata map[string]any) {
		metadata["token_endpoint_auth_methods_supported"] = []string{"client_secret_basic"}
	}, func() map[string]any { return map[string]any{} })
	if _, err := resolveEnterpriseAuthorizationMetadata(ctx, noPublicAuth.URL, "client", "", "", nil); err == nil ||
		!strings.Contains(err.Error(), "public-client") {
		t.Fatalf("public-client error = %v", err)
	}

	// An issuer with no published metadata is rejected.
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer empty.Close()
	if _, err := resolveEnterpriseAuthorizationMetadata(ctx, empty.URL, "client", "", "", nil); err == nil ||
		!strings.Contains(err.Error(), "must publish authorization metadata") {
		t.Fatalf("missing metadata error = %v", err)
	}
}

// TestStartEnterpriseOAuthLoginStagesCredentials covers Rust #43844's staged
// login: the authorization URL drops resource indicators and forces consent,
// browser completion validates the grant without storing it, and only the
// explicit commit persists the credential.
func TestStartEnterpriseOAuthLoginStagesCredentials(t *testing.T) {
	ctx := context.Background()
	clientID := "eci-prd-pub-codex-123"
	var server *httptest.Server
	server = enterpriseTestIdP(t, nil, func() map[string]any {
		return map[string]any{
			"access_token":  "access-1",
			"token_type":    "Bearer",
			"refresh_token": "refresh-1",
			"id_token":      mcpEMATestJWT(t, map[string]any{"alg": "ES256"}, mcpEMATestClaims(server.URL, clientID, "")),
		}
	})
	home := t.TempDir()
	handle, err := StartEnterpriseOAuthLogin(ctx, &EnterpriseOAuthLoginOptions{
		CodexHome:      home,
		CredentialName: "enterprise",
		Issuer:         server.URL,
		ClientID:       clientID,
		RedirectURL:    "http://127.0.0.1:1455/callback",
	})
	if err != nil {
		t.Fatalf("start enterprise login: %v", err)
	}
	authURL, err := url.Parse(handle.AuthorizationURL())
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	if authURL.Query().Get("prompt") != "consent" || authURL.Query().Has("resource") {
		t.Fatalf("authorization URL = %q", handle.AuthorizationURL())
	}
	if state := authURL.Query().Get("state"); state == "" || state != handle.session.State {
		t.Fatalf("authorization state = %q, want %q", state, handle.session.State)
	}

	credentials, err := handle.Complete(ctx, "/callback?code=code-1&state="+url.QueryEscape(handle.session.State))
	if err != nil {
		t.Fatalf("complete enterprise login: %v", err)
	}
	if credentials.Tokens == nil || credentials.Tokens.RefreshToken != "refresh-1" {
		t.Fatalf("staged credentials = %#v", credentials.Tokens)
	}
	// Browser completion alone must not persist the grant.
	if loaded, err := NewOAuthStore(home).Load("enterprise", server.URL); err != nil || loaded != nil {
		t.Fatalf("grant before commit = (%#v, %v), want none", loaded, err)
	}

	authority, err := CommitEnterpriseOAuthCredentials(home, credentials.Tokens, credentials.CommitGeneration(), func() *string {
		value := "account-1"
		return &value
	}, OAuthCredentialsStoreAuto)
	if err != nil || authority != "account-1" {
		t.Fatalf("commit = (%q, %v)", authority, err)
	}
	if loaded, err := NewOAuthStore(home).Load("enterprise", server.URL); err != nil || loaded == nil || loaded.AccessToken != "access-1" {
		t.Fatalf("stored grant = (%#v, %v)", loaded, err)
	}
}

// TestStartEnterpriseOAuthLoginConfidentialClientLikeRust mirrors #56001: a
// provisioned IdP secret authenticates the token request with the registered
// Basic method, keeps its exact value, and never reaches the authorization URL
// or the staged and stored grant.
func TestStartEnterpriseOAuthLoginConfidentialClientLikeRust(t *testing.T) {
	ctx := context.Background()
	clientID := "eci-prd-confidential-123"
	clientSecret := "pa ss&word "
	var authorizationHeader string
	var tokenForm url.Values
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case mcpOAuthAuthorizationServerWellKnownPath:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                "http://" + r.Host,
				"authorization_endpoint":                "http://" + r.Host + "/authorize",
				"token_endpoint":                        "http://" + r.Host + "/token",
				"token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
			})
		case "/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, "form", http.StatusBadRequest)
				return
			}
			authorizationHeader = r.Header.Get("Authorization")
			tokenForm = r.PostForm
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access-1",
				"token_type":    "Bearer",
				"refresh_token": "refresh-1",
				"id_token":      mcpEMATestJWT(t, map[string]any{"alg": "ES256"}, mcpEMATestClaims(server.URL, clientID, "")),
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	home := t.TempDir()
	handle, err := StartEnterpriseOAuthLogin(ctx, &EnterpriseOAuthLoginOptions{
		CodexHome:                 home,
		CredentialName:            "enterprise",
		Issuer:                    server.URL,
		ClientID:                  clientID,
		ClientSecret:              clientSecret,
		RedirectURL:               "http://127.0.0.1:1455/callback",
		OAuthCredentialsStoreMode: OAuthCredentialsStoreFile,
	})
	if err != nil {
		t.Fatalf("start enterprise login: %v", err)
	}
	if strings.Contains(handle.AuthorizationURL(), "client_secret") {
		t.Fatalf("authorization URL leaked the client secret: %q", handle.AuthorizationURL())
	}
	if handle.session.ClientSecret != clientSecret {
		t.Fatalf("session secret = %q, want the exact configured value", handle.session.ClientSecret)
	}

	credentials, err := handle.Complete(ctx, "/callback?code=code-1&state="+url.QueryEscape(handle.session.State))
	if err != nil {
		t.Fatalf("complete enterprise login: %v", err)
	}
	if credentials.Tokens == nil || credentials.Tokens.RefreshToken != "refresh-1" {
		t.Fatalf("staged credentials = %#v", credentials.Tokens)
	}
	if credentials.Tokens.ClientSecret != "" {
		t.Fatalf("staged grant kept the client secret: %q", credentials.Tokens.ClientSecret)
	}

	if !strings.HasPrefix(authorizationHeader, "Basic ") {
		t.Fatalf("token request Authorization = %q, want Basic", authorizationHeader)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(authorizationHeader, "Basic "))
	if err != nil {
		t.Fatalf("decode Basic credentials: %v", err)
	}
	if want := url.QueryEscape(clientID) + ":" + url.QueryEscape(clientSecret); string(decoded) != want {
		t.Fatalf("Basic credentials = %q, want %q", string(decoded), want)
	}
	if _, ok := tokenForm["client_id"]; ok {
		t.Fatalf("Basic token request leaked client_id into the form: %#v", tokenForm)
	}
	if _, ok := tokenForm["client_secret"]; ok {
		t.Fatalf("Basic token request leaked client_secret into the form: %#v", tokenForm)
	}

	if _, err := CommitEnterpriseOAuthCredentials(home, credentials.Tokens, credentials.CommitGeneration(), func() *string {
		value := "account-1"
		return &value
	}, OAuthCredentialsStoreFile); err != nil {
		t.Fatalf("commit enterprise credentials: %v", err)
	}
	loaded, err := NewOAuthStoreWithMode(home, OAuthCredentialsStoreFile).Load("enterprise", server.URL)
	if err != nil || loaded == nil {
		t.Fatalf("stored grant = (%#v, %v)", loaded, err)
	}
	if loaded.ClientSecret != "" {
		t.Fatalf("stored grant kept the client secret: %q", loaded.ClientSecret)
	}
	file, err := os.ReadFile(filepath.Join(home, mcpOAuthFallbackFilename))
	if err != nil {
		t.Fatalf("read credentials file: %v", err)
	}
	if strings.Contains(string(file), clientSecret) {
		t.Fatal("stored credentials file leaked the client secret")
	}
}

// TestEnterpriseOAuthLoginRejectsIncompleteGrant covers the staged validation:
// a token response without a refresh token or OIDC identity assertion cannot be
// staged.
func TestEnterpriseOAuthLoginRejectsIncompleteGrant(t *testing.T) {
	ctx := context.Background()
	server := enterpriseTestIdP(t, nil, func() map[string]any {
		return map[string]any{"access_token": "access-1", "token_type": "Bearer"}
	})
	handle, err := StartEnterpriseOAuthLogin(ctx, &EnterpriseOAuthLoginOptions{
		CodexHome:      t.TempDir(),
		CredentialName: "enterprise",
		Issuer:         server.URL,
		ClientID:       "client-1",
		RedirectURL:    "http://127.0.0.1:1455/callback",
	})
	if err != nil {
		t.Fatalf("start enterprise login: %v", err)
	}
	if _, err := handle.Complete(ctx, "/callback?code=code-1&state="+url.QueryEscape(handle.session.State)); err == nil {
		t.Fatal("a grant without a refresh token must not be staged")
	}
}

// TestOAuthTokenSetCapturesIDToken covers Rust #43844's prerequisite: the OIDC
// identity assertion returned by the exchange survives into the stored grant
// so enterprise credentials can be validated after login.
func TestOAuthTokenSetCapturesIDToken(t *testing.T) {
	token := &oauth2.Token{
		AccessToken:  "access",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(time.Hour),
	}
	token = token.WithExtra(map[string]any{"id_token": "  assertion-value  "})
	tokens := oauth2TokenToSet(token, &oauthTokenSetOptions{
		ServerName: "enterprise", ServerURL: "https://idp.example.com", ClientID: "client-1",
	})
	if tokens == nil || tokens.IDToken != "assertion-value" {
		t.Fatalf("captured id_token = %#v", tokens)
	}
	entry := oauthFallbackEntryFromTokenSet(tokens)
	restored := oauthTokenSetFromFallbackEntry(entry)
	if restored.IDToken != "assertion-value" {
		t.Fatalf("persisted id_token = %q", restored.IDToken)
	}
}

// TestEnterpriseCallbackSettingsMatchesRust covers Rust #43844's callback
// policy and the expanded #44616 matrix: a registered client ID and an HTTP
// loopback callback are required, and the URL and listener must agree on the
// port.
func TestEnterpriseCallbackSettingsMatchesRust(t *testing.T) {
	const issuer = "https://idp.example.com"
	const clientID = "client-1"
	port := func(value uint16) *uint16 { return &value }
	tests := []struct {
		name         string
		issuer       string
		clientID     string
		callbackURL  string
		callbackPort *uint16
		wantIP       string
		wantPort     *uint16
		wantErr      bool
	}{
		{name: "default", issuer: issuer, clientID: clientID, wantIP: "127.0.0.1"},
		{name: "listener port only", issuer: issuer, clientID: clientID, callbackPort: port(8080), wantIP: "127.0.0.1", wantPort: port(8080)},
		{name: "localhost", issuer: issuer, clientID: clientID, callbackURL: "http://localhost/callback", wantIP: "127.0.0.1"},
		{name: "alternate IPv4 loopback", issuer: issuer, clientID: clientID, callbackURL: "http://127.0.0.2/callback", wantIP: "127.0.0.2"},
		{name: "IPv6 loopback", issuer: issuer, clientID: clientID, callbackURL: "http://[::1]/callback", wantIP: "::1"},
		{name: "URL port", issuer: issuer, clientID: clientID, callbackURL: "http://localhost:8080/callback", wantIP: "127.0.0.1", wantPort: port(8080)},
		{name: "matching URL and listener port", issuer: issuer, clientID: clientID, callbackURL: "http://localhost:8080/callback", callbackPort: port(8080), wantIP: "127.0.0.1", wantPort: port(8080)},
		{name: "conflicting URL and listener port", issuer: issuer, clientID: clientID, callbackURL: "http://localhost:8080/callback", callbackPort: port(9090), wantErr: true},
		{name: "missing client ID", issuer: issuer, clientID: "   ", wantErr: true},
		{name: "non-HTTPS issuer", issuer: "http://idp.example.com", clientID: clientID, wantErr: true},
		{name: "HTTPS callback", issuer: issuer, clientID: clientID, callbackURL: "https://idp.example.com/callback", wantErr: true},
		{name: "remote callback host", issuer: issuer, clientID: clientID, callbackURL: "http://example.com/callback", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip, gotPort, err := enterpriseCallbackSettings(tt.issuer, tt.clientID, tt.callbackURL, tt.callbackPort)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("enterpriseCallbackSettings(%q, %q, %q, %v) succeeded, want error", tt.issuer, tt.clientID, tt.callbackURL, tt.callbackPort)
				}
				return
			}
			if err != nil {
				t.Fatalf("enterpriseCallbackSettings(%q, %q, %q, %v): %v", tt.issuer, tt.clientID, tt.callbackURL, tt.callbackPort, err)
			}
			wantIP := net.ParseIP(tt.wantIP)
			if !ip.Equal(wantIP) {
				t.Fatalf("IP = %v, want %v", ip, wantIP)
			}
			if tt.wantPort == nil {
				if gotPort != nil {
					t.Fatalf("port = %d, want nil", *gotPort)
				}
				return
			}
			if gotPort == nil || *gotPort != *tt.wantPort {
				t.Fatalf("port = %v, want %d", gotPort, *tt.wantPort)
			}
		})
	}
}

// TestEnterpriseAuthorizationURLMatchesRust covers Rust #43844: resource
// indicators and any existing prompt are dropped and `prompt=consent` is
// appended, preserving the remaining parameters.
func TestEnterpriseAuthorizationURLMatchesRust(t *testing.T) {
	got, err := enterpriseAuthorizationURL("https://idp.example.com/authorize?client_id=abc&resource=https%3A%2F%2Fidp.example.com&scope=openid+offline_access&prompt=none&state=xyz")
	if err != nil {
		t.Fatalf("enterprise authorization URL: %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}
	values := parsed.Query()
	if values.Get("resource") != "" || values.Get("prompt") != "consent" {
		t.Fatalf("resource/prompt handling = %v", parsed.RawQuery)
	}
	if values.Get("client_id") != "abc" || values.Get("scope") != "openid offline_access" || values.Get("state") != "xyz" {
		t.Fatalf("kept params = %v", parsed.RawQuery)
	}
	if !strings.HasSuffix(parsed.RawQuery, "prompt=consent") {
		t.Fatalf("prompt must be appended: %q", parsed.RawQuery)
	}
}

// TestValidateEnterpriseOAuthCredentialsMatchesRust covers the staged-login
// checks: a refresh token and a valid OIDC identity assertion are required,
// and the error text never carries credential data.
func TestValidateEnterpriseOAuthCredentialsMatchesRust(t *testing.T) {
	clientID := "client-1"
	issuer := "https://idp.example.com"
	assertion := mcpEMATestJWT(t, map[string]any{"alg": "ES256"}, mcpEMATestClaims(issuer, clientID, ""))
	valid := &OAuthTokenSet{
		ServerName:   "enterprise",
		ServerURL:    issuer,
		ClientID:     clientID,
		Issuer:       issuer,
		RefreshToken: "refresh-1",
		IDToken:      assertion,
	}
	if err := validateEnterpriseOAuthCredentials(valid); err != nil {
		t.Fatalf("valid credentials rejected: %v", err)
	}
	if err := validateEnterpriseOAuthCredentials(&OAuthTokenSet{ServerURL: issuer, ClientID: clientID, IDToken: assertion}); err == nil {
		t.Fatal("missing refresh token must be rejected")
	}
	if err := validateEnterpriseOAuthCredentials(&OAuthTokenSet{ServerURL: issuer, ClientID: clientID, RefreshToken: " \t ", IDToken: assertion}); err == nil {
		t.Fatal("a whitespace-only refresh token must be rejected")
	}
	if err := validateEnterpriseOAuthCredentials(&OAuthTokenSet{ServerURL: issuer, ClientID: clientID, RefreshToken: "refresh-1"}); err == nil {
		t.Fatal("missing OIDC identity assertion must be rejected")
	}
	wrongAudience := mcpEMATestJWT(t, map[string]any{"alg": "ES256"}, mcpEMATestClaims(issuer, "other-client", ""))
	if err := validateEnterpriseOAuthCredentials(&OAuthTokenSet{
		ServerURL: issuer, ClientID: clientID, RefreshToken: "refresh-1", IDToken: wrongAudience,
	}); err == nil {
		t.Fatal("a mismatched audience must be rejected")
	}
}

// TestDeleteEnterpriseOAuthTokensInvalidatesStagedAttempt covers Rust #43844's
// coordinated logout: the grant is removed and an earlier staged attempt is
// invalidated even when no grant was stored.
func TestDeleteEnterpriseOAuthTokensInvalidatesStagedAttempt(t *testing.T) {
	home := t.TempDir()
	const name = "enterprise"
	const issuer = "https://idp.example.com"

	attempt, err := AcquireEnterpriseOAuthCredentialGuard(home, name, issuer, OAuthCredentialsStoreAuto)
	if err != nil {
		t.Fatalf("acquire attempt guard: %v", err)
	}
	staged, err := attempt.generationFile.replace()
	if err != nil {
		t.Fatalf("stage generation: %v", err)
	}
	// The staged attempt captured the generation and later dropped its lock.
	attempt.Close()

	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName: name, ServerURL: issuer, ClientID: "client-1",
		AccessToken: "access", RefreshToken: "refresh",
	}); err != nil {
		t.Fatalf("save grant: %v", err)
	}

	removed, err := DeleteEnterpriseOAuthTokens(home, name, issuer, OAuthCredentialsStoreAuto)
	if err != nil || !removed {
		t.Fatalf("delete enterprise tokens = (%v, %v), want (true, nil)", removed, err)
	}
	if loaded, err := NewOAuthStore(home).Load(name, issuer); err != nil || loaded != nil {
		t.Fatalf("grant after logout = (%#v, %v), want removed", loaded, err)
	}

	// The staged attempt is stale: its generation no longer matches.
	stale, err := AcquireEnterpriseOAuthCredentialGuard(home, name, issuer, OAuthCredentialsStoreAuto)
	if err != nil {
		t.Fatalf("reacquire guard: %v", err)
	}
	current, ok, err := stale.generationFile.current()
	if err != nil || !ok {
		t.Fatalf("current generation = (ok=%v, err=%v)", ok, err)
	}
	stale.Close()
	if current == staged {
		t.Fatal("logout must replace the generation so staged attempts are invalid")
	}

	// A second logout has no grant but still reports false without error.
	removed, err = DeleteEnterpriseOAuthTokens(home, name, issuer, OAuthCredentialsStoreAuto)
	if err != nil || removed {
		t.Fatalf("second delete = (%v, %v), want (false, nil)", removed, err)
	}
}

// TestCommitEnterpriseOAuthCredentialsMatchesRust covers Rust #43844's staged
// commit: the generation and caller authority are rechecked under the lock
// before the grant is persisted.
func TestCommitEnterpriseOAuthCredentialsMatchesRust(t *testing.T) {
	home := t.TempDir()
	const name = "enterprise"
	const issuer = "https://idp.example.com"
	tokens := &OAuthTokenSet{
		ServerName: name, ServerURL: issuer, ClientID: "client-1",
		AccessToken: "access", RefreshToken: "refresh",
	}

	guard, err := AcquireEnterpriseOAuthCredentialGuard(home, name, issuer, OAuthCredentialsStoreAuto)
	if err != nil {
		t.Fatalf("acquire guard: %v", err)
	}
	generation, err := guard.generationFile.replace()
	if err != nil {
		t.Fatalf("initialize generation: %v", err)
	}
	guard.Close()

	authority, err := CommitEnterpriseOAuthCredentials(home, tokens, generation, func() *string {
		value := "account-1"
		return &value
	}, OAuthCredentialsStoreAuto)
	if err != nil || authority != "account-1" {
		t.Fatalf("commit = (%q, %v)", authority, err)
	}
	if loaded, err := NewOAuthStore(home).Load(name, issuer); err != nil || loaded == nil || loaded.AccessToken != "access" {
		t.Fatalf("stored grant = (%#v, %v)", loaded, err)
	}

	// A stale generation is rejected before the authority check runs.
	staleGeneration := generation
	staleGeneration[0] ^= 0xff
	called := false
	if _, err := CommitEnterpriseOAuthCredentials(home, tokens, staleGeneration, func() *string {
		called = true
		return nil
	}, OAuthCredentialsStoreAuto); err == nil {
		t.Fatal("a stale generation must be rejected")
	}
	if called {
		t.Fatal("the authority check must not run for a stale attempt")
	}

	// A current generation with a nil authority proof is rejected.
	if _, err := CommitEnterpriseOAuthCredentials(home, tokens, generation, func() *string {
		return nil
	}, OAuthCredentialsStoreAuto); err == nil {
		t.Fatal("a nil authority proof must be rejected")
	}
}
