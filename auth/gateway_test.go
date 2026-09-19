package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/keyring"
)

// authFakeKeyring is an in-memory keyring so gateway tests never touch the OS store.
type authFakeKeyring struct {
	values map[string]string
}

func newGatewayTestStore() *authFakeKeyring {
	return &authFakeKeyring{values: map[string]string{}}
}

func (s *authFakeKeyring) key(service string, account string) string {
	return service + "\x00" + account
}

func (s *authFakeKeyring) Load(service string, account string) (string, error) {
	value, ok := s.values[s.key(service, account)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (s *authFakeKeyring) Save(service string, account string, secret string) error {
	s.values[s.key(service, account)] = secret
	return nil
}

func (s *authFakeKeyring) Delete(service string, account string) (bool, error) {
	key := s.key(service, account)
	if _, ok := s.values[key]; !ok {
		return false, nil
	}
	delete(s.values, key)
	return true, nil
}

// Mirrors Rust gateway_auth_storage: the account id is a provider-oauth digest and
// the secret name is PROVIDER_OAUTH_<UPPER DIGEST>; anything else is rejected.
func TestGatewayCredentialIdentityLikeRust(t *testing.T) {
	config := gatewayAuthConfig{
		authorizationURL: "https://issuer.example.com/authorize",
		tokenURL:         "https://issuer.example.com/token",
		clientID:         "client-1",
		resource:         "https://api.example.com",
		scopes:           []string{"openid", "profile"},
	}
	id := gatewayCredentialID("/home/user/.codex", config)
	if !strings.HasPrefix(id, "provider-oauth|") || len(id) != len("provider-oauth|")+64 {
		t.Fatalf("credential id = %q", id)
	}
	if other := gatewayCredentialID("/home/user/.codex", gatewayAuthConfig{authorizationURL: "https://other.example.com"}); other == id {
		t.Fatal("different configurations must have different credential ids")
	}
	name, err := gatewayCredentialSecretName(id)
	if err != nil {
		t.Fatalf("gatewayCredentialSecretName() error = %v", err)
	}
	if !strings.HasPrefix(name.String(), "PROVIDER_OAUTH_") || name.String() != strings.ToUpper(name.String()) {
		t.Fatalf("secret name = %q", name.String())
	}
	if _, err := gatewayCredentialSecretName("not-provider-oauth"); err == nil {
		t.Fatal("a foreign account id must be rejected")
	}
	if _, err := gatewayCredentialSecretName("provider-oauth|"); err == nil {
		t.Fatal("an empty digest must be rejected")
	}
}

// Mirrors Rust storage: the credentials are stored in the gateway namespace and
// the sidecar lock is exclusive until it is released.
func TestGatewayStorageAndLockLikeRust(t *testing.T) {
	home := t.TempDir()
	storage := newGatewayAuthStorage(home, newGatewayTestStore())
	id := gatewayCredentialID(home, gatewayAuthConfig{tokenURL: "https://issuer.example.com/token"})
	if value, ok, err := storage.load(id); err != nil || ok || value != "" {
		t.Fatalf("initial load = %q, %v, %v", value, ok, err)
	}
	if err := storage.save(id, `{"access_token":"token-1"}`); err != nil {
		t.Fatalf("save() error = %v", err)
	}
	value, ok, err := storage.load(id)
	if err != nil || !ok || value != `{"access_token":"token-1"}` {
		t.Fatalf("load() = %q, %v, %v", value, ok, err)
	}
	if _, err := os.Stat(filepath.Join(home, "secrets", "gateway_oauth.age")); err != nil {
		t.Fatalf("gateway namespace file missing: %v", err)
	}

	lock, err := lockGatewayCredentials(home)
	if err != nil {
		t.Fatalf("lockGatewayCredentials() error = %v", err)
	}
	second, acquired, err := tryAcquireGatewayCredentialLock(filepath.Join(home, "secrets", "gateway_oauth.lock"))
	if err != nil {
		t.Fatalf("second acquire error = %v", err)
	}
	if acquired {
		_ = second.release()
		t.Fatal("the credential lock was not exclusive")
	}
	if err := lock.release(); err != nil {
		t.Fatalf("release() error = %v", err)
	}
	third, acquired, err := tryAcquireGatewayCredentialLock(filepath.Join(home, "secrets", "gateway_oauth.lock"))
	if err != nil || !acquired {
		t.Fatalf("re-acquire = %v, %v", acquired, err)
	}
	_ = third.release()
}

// Mirrors Rust gateway_auth_token: the response must carry an access token, a
// bearer type when present, and a non-zero lifetime; the refresh token rotates
// while a missing replacement keeps the previous value.
func TestGatewayTokenResponseLikeRust(t *testing.T) {
	lifetime := uint64(3600)
	stored, err := gatewayTokenResponse{
		AccessToken: "token-1", TokenType: "Bearer", ExpiresIn: &lifetime, RefreshToken: "refresh-2",
	}.intoStored("refresh-1")
	if err != nil {
		t.Fatalf("intoStored() error = %v", err)
	}
	if stored.AccessToken != "token-1" || stored.RefreshToken != "refresh-2" || stored.ExpiresAt == nil {
		t.Fatalf("stored = %#v", stored)
	}
	if !gatewayTokenIsUsable(stored) {
		t.Fatal("a fresh token must be usable")
	}
	kept, err := gatewayTokenResponse{AccessToken: "token-2"}.intoStored("refresh-1")
	if err != nil || kept.RefreshToken != "refresh-1" || kept.ExpiresAt != nil {
		t.Fatalf("missing rotation = %#v, %v", kept, err)
	}
	expired := time.Now().Unix() - 1
	if gatewayTokenIsUsable(gatewayStoredToken{AccessToken: "old", ExpiresAt: &expired}) {
		t.Fatal("an expired token must not be usable")
	}
	if gatewayTokenIsUsable(gatewayStoredToken{AccessToken: "skewed", ExpiresAt: ptrInt64(time.Now().Unix() + 10)}) {
		t.Fatal("a token inside the refresh skew must not be usable")
	}
	for name, response := range map[string]gatewayTokenResponse{
		"missing access token": {},
		"wrong type":           {AccessToken: "t", TokenType: "mac"},
		"zero lifetime":        {AccessToken: "t", ExpiresIn: ptrUint64(0)},
	} {
		if _, err := response.intoStored(""); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

// Mirrors Rust validate_config.
func TestValidateGatewayAuthConfigLikeRust(t *testing.T) {
	valid := gatewayAuthConfig{
		authorizationURL: "https://issuer.example.com/authorize",
		tokenURL:         "https://issuer.example.com/token",
		clientID:         "client-1",
	}
	if err := validateGatewayAuthConfig(valid); err != nil {
		t.Fatalf("valid config error = %v", err)
	}
	loopback := valid
	loopback.authorizationURL = "http://127.0.0.1:1455/authorize"
	loopback.tokenURL = "http://localhost:1455/token"
	if err := validateGatewayAuthConfig(loopback); err != nil {
		t.Fatalf("loopback config error = %v", err)
	}
	for name, config := range map[string]gatewayAuthConfig{
		"non-loopback http":  {authorizationURL: "http://issuer.example.com/a", tokenURL: "https://issuer.example.com/t", clientID: "c"},
		"embedded creds":     {authorizationURL: "https://user:pass@issuer.example.com/a", tokenURL: "https://issuer.example.com/t", clientID: "c"},
		"fragment":           {authorizationURL: "https://issuer.example.com/a#frag", tokenURL: "https://issuer.example.com/t", clientID: "c"},
		"oauth params":       {authorizationURL: "https://issuer.example.com/a?client_id=x", tokenURL: "https://issuer.example.com/t", clientID: "c"},
		"empty client":       {authorizationURL: "https://issuer.example.com/a", tokenURL: "https://issuer.example.com/t"},
		"zero redirect port": {authorizationURL: "https://issuer.example.com/a", tokenURL: "https://issuer.example.com/t", clientID: "c", redirectPortSet: true},
	} {
		if err := validateGatewayAuthConfig(config); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

// Mirrors Rust GatewayAuthManager end to end: an expired stored token refreshes,
// a rejection reuses a newer stored replacement, and an unusable stored refresh
// token falls back to the browser authorize flow.
func TestGatewayAuthManagerRefreshAndAuthorizeLikeRust(t *testing.T) {
	var (
		refreshCalls int
		lastRefresh  url.Values
	)
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		form, _ := url.ParseQuery(string(body))
		writer.Header().Set("Content-Type", "application/json")
		switch form.Get("grant_type") {
		case "refresh_token":
			refreshCalls++
			lastRefresh = form
			if form.Get("refresh_token") == "rejected-refresh" {
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = writer.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			_, _ = writer.Write([]byte(`{"access_token":"refreshed-token","token_type":"bearer","expires_in":3600}`))
		case "authorization_code":
			_, _ = writer.Write([]byte(`{"access_token":"authorized-token","token_type":"bearer","expires_in":3600,"refresh_token":"refresh-new"}`))
		default:
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":"unsupported_grant_type"}`))
		}
	}))
	defer issuer.Close()

	home := t.TempDir()
	store := newGatewayTestStore()
	config := gatewayAuthConfig{
		authorizationURL: issuer.URL + "/authorize",
		tokenURL:         issuer.URL + "/token",
		clientID:         "client-1",
	}
	manager := newGatewayAuthManager(config, home, issuer.Client(), store)

	// Authorize: the injected browser opener answers the loopback callback.
	manager.openBrowser = func(authorizationURL string) error {
		parsed, err := url.Parse(authorizationURL)
		if err != nil {
			return err
		}
		go func() {
			response, err := http.Get(parsed.Query().Get("redirect_uri") + "?code=code-1&state=" + url.QueryEscape(parsed.Query().Get("state")))
			if err == nil {
				_, _ = io.ReadAll(response.Body)
				_ = response.Body.Close()
			}
		}()
		return nil
	}
	token, err := manager.resolveAccessToken(context.Background())
	if err != nil || token != "authorized-token" {
		t.Fatalf("authorize = %q, %v", token, err)
	}
	// The credential is persisted under the provider-oauth account id.
	value, ok, err := manager.storage.load(gatewayCredentialID(home, config))
	if err != nil || !ok || !strings.Contains(value, "authorized-token") {
		t.Fatalf("stored credentials = %q, %v, %v", value, ok, err)
	}

	// Cache reuse: a second resolve returns the same token without new grants.
	if token, err := manager.resolveAccessToken(context.Background()); err != nil || token != "authorized-token" {
		t.Fatalf("cached resolve = %q, %v", token, err)
	}
	if refreshCalls != 0 {
		t.Fatalf("refresh calls after cache hit = %d", refreshCalls)
	}

	// An explicitly rejected token must refresh, and the rotated refresh token
	// is persisted.
	rejected, err := manager.refreshAccessToken(context.Background(), "authorized-token")
	if err != nil || rejected != "refreshed-token" {
		t.Fatalf("refresh after rejection = %q, %v", rejected, err)
	}
	if lastRefresh.Get("grant_type") != "refresh_token" || lastRefresh.Get("refresh_token") != "refresh-new" || lastRefresh.Get("resource") != "" {
		t.Fatalf("refresh form = %v", lastRefresh)
	}
	value, _, _ = manager.storage.load(gatewayCredentialID(home, config))
	if !strings.Contains(value, "refreshed-token") {
		t.Fatalf("rotated credentials = %q", value)
	}
}

// Mirrors Rust's refresh rejection handling: a rejected refresh token falls back
// to the browser authorize flow instead of failing the caller.
func TestGatewayAuthManagerReauthorizesAfterRefreshRejectionLikeRust(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		form, _ := url.ParseQuery(string(body))
		writer.Header().Set("Content-Type", "application/json")
		if form.Get("grant_type") == "authorization_code" {
			_, _ = writer.Write([]byte(`{"access_token":"authorized-token","token_type":"bearer","expires_in":3600}`))
			return
		}
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token expired"}`))
	}))
	defer issuer.Close()

	home := t.TempDir()
	store := newGatewayTestStore()
	config := gatewayAuthConfig{
		authorizationURL: issuer.URL + "/authorize",
		tokenURL:         issuer.URL + "/token",
		clientID:         "client-1",
	}
	manager := newGatewayAuthManager(config, home, issuer.Client(), store)
	// A stored token with an unusable refresh token.
	expired := time.Now().Unix() - 60
	payload, _ := json.Marshal(gatewayStoredToken{AccessToken: "stale", RefreshToken: "rejected-refresh", ExpiresAt: &expired})
	if err := manager.storage.save(gatewayCredentialID(home, config), string(payload)); err != nil {
		t.Fatalf("save() error = %v", err)
	}
	manager.openBrowser = func(authorizationURL string) error {
		parsed, _ := url.Parse(authorizationURL)
		go func() {
			response, err := http.Get(parsed.Query().Get("redirect_uri") + "?code=code-2&state=" + url.QueryEscape(parsed.Query().Get("state")))
			if err == nil {
				_, _ = io.ReadAll(response.Body)
				_ = response.Body.Close()
			}
		}()
		return nil
	}
	token, err := manager.resolveAccessToken(context.Background())
	if err != nil || token != "authorized-token" {
		t.Fatalf("resolve after refresh rejection = %q, %v", token, err)
	}
}

func ptrInt64(value int64) *int64    { return &value }
func ptrUint64(value uint64) *uint64 { return &value }
