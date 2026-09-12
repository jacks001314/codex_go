package mcp

import (
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

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
// policy: a registered client ID and an HTTP loopback callback are required,
// and the URL and listener must agree on the port.
func TestEnterpriseCallbackSettingsMatchesRust(t *testing.T) {
	ip, port, err := enterpriseCallbackSettings("https://idp.example.com", "client-1", "", nil)
	if err != nil {
		t.Fatalf("default callback settings: %v", err)
	}
	if !ip.Equal(net.IPv4(127, 0, 0, 1)) || port != nil {
		t.Fatalf("default = (%v, %v), want (127.0.0.1, nil)", ip, port)
	}

	ip, port, err = enterpriseCallbackSettings("https://idp.example.com", "client-1", "http://localhost:1455/callback", nil)
	if err != nil {
		t.Fatalf("localhost callback: %v", err)
	}
	if !ip.Equal(net.IPv4(127, 0, 0, 1)) || port == nil || *port != 1455 {
		t.Fatalf("localhost callback = (%v, %v), want (127.0.0.1, 1455)", ip, port)
	}

	ip, port, err = enterpriseCallbackSettings("https://idp.example.com", "client-1", "http://127.0.0.1:1455/callback", nil)
	if err != nil {
		t.Fatalf("loopback IPv4 callback: %v", err)
	}
	if !ip.Equal(net.IPv4(127, 0, 0, 1)) || port == nil || *port != 1455 {
		t.Fatalf("loopback IPv4 = (%v, %v)", ip, port)
	}

	listener := uint16(1456)
	if _, _, err := enterpriseCallbackSettings("https://idp.example.com", "client-1", "http://127.0.0.1:1455/callback", &listener); err == nil {
		t.Fatal("a URL/listener port mismatch must be rejected")
	}
	listener = 1455
	if _, port, err := enterpriseCallbackSettings("https://idp.example.com", "client-1", "http://127.0.0.1:1455/callback", &listener); err != nil || port == nil || *port != 1455 {
		t.Fatalf("matching listener = (%v, %v)", port, err)
	}

	if _, _, err := enterpriseCallbackSettings("https://idp.example.com", "   ", "", nil); err == nil {
		t.Fatal("a missing registered client ID must be rejected")
	}
	if _, _, err := enterpriseCallbackSettings("http://idp.example.com", "client-1", "", nil); err == nil {
		t.Fatal("a non-HTTPS issuer must be rejected")
	}
	if _, _, err := enterpriseCallbackSettings("https://idp.example.com", "client-1", "https://idp.example.com/callback", nil); err == nil {
		t.Fatal("a non-loopback callback URL must be rejected")
	}
	if _, _, err := enterpriseCallbackSettings("https://idp.example.com", "client-1", "http://example.com/callback", nil); err == nil {
		t.Fatal("a non-loopback host must be rejected")
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

	attempt, err := AcquireEnterpriseOAuthCredentialGuard(home, name, issuer)
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

	removed, err := DeleteEnterpriseOAuthTokens(home, name, issuer)
	if err != nil || !removed {
		t.Fatalf("delete enterprise tokens = (%v, %v), want (true, nil)", removed, err)
	}
	if loaded, err := NewOAuthStore(home).Load(name, issuer); err != nil || loaded != nil {
		t.Fatalf("grant after logout = (%#v, %v), want removed", loaded, err)
	}

	// The staged attempt is stale: its generation no longer matches.
	stale, err := AcquireEnterpriseOAuthCredentialGuard(home, name, issuer)
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
	removed, err = DeleteEnterpriseOAuthTokens(home, name, issuer)
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

	guard, err := AcquireEnterpriseOAuthCredentialGuard(home, name, issuer)
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
	})
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
	}); err == nil {
		t.Fatal("a stale generation must be rejected")
	}
	if called {
		t.Fatal("the authority check must not run for a stale attempt")
	}

	// A current generation with a nil authority proof is rejected.
	if _, err := CommitEnterpriseOAuthCredentials(home, tokens, generation, func() *string {
		return nil
	}); err == nil {
		t.Fatal("a nil authority proof must be rejected")
	}
}
