package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// registryCredentialRegistrationBody is a well-formed environment registry
// registration response, optionally echoing the executor credential exactly like
// the upstream fixture does (Rust #54346
// `executor_credential_is_never_returned_in_registry_diagnostics`).
func registryCredentialRegistrationBody(environmentID, credential string) string {
	body := map[string]any{
		"environment_id":           environmentID,
		"url":                      "wss://registry.example.test/rendezvous",
		"security_profile":         RemoteSecurityProfile,
		"executor_registration_id": "registration-1",
	}
	if credential != "" {
		body["credential_echo"] = "echoed " + credential
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func registryCredentialConfig(baseURL, environmentID string, headers http.Header) RemoteEnvironmentConfig {
	return RemoteEnvironmentConfig{
		BaseURL:       baseURL,
		EnvironmentID: environmentID,
		AuthHeaders:   headers,
	}
}

func bearerHeaders(token string) http.Header {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	return headers
}

// Rust #54346 (`remote.rs`, `RegistryAuthMode::ExecutorCredential`): the scoped
// mode accepts exactly one `Authorization: Bearer <token>` header, and every
// other shape is rejected before any request is built.
func TestScopedExecutorCredentialHeadersLikeRust(t *testing.T) {
	const baseURL = "https://registry.example.test"

	if _, credential, err := resolveRemoteEnvironmentAuth(context.Background(),
		registryCredentialConfig(baseURL, "env-1", bearerHeaders("synthetic-scoped-token"))); err != nil {
		t.Fatalf("legacy resolve error = %v", err)
	} else if credential != "" {
		t.Fatalf("legacy mode returned credential %q, want none", credential)
	}

	scoped, err := registryCredentialConfig(baseURL, "env-1", bearerHeaders("synthetic-scoped-token")).WithExecutorCredentialAuth()
	if err != nil {
		t.Fatalf("WithExecutorCredentialAuth() error = %v", err)
	}
	headers, credential, err := resolveRemoteEnvironmentAuth(context.Background(), scoped)
	if err != nil {
		t.Fatalf("scoped resolve error = %v", err)
	}
	if credential != "synthetic-scoped-token" {
		t.Fatalf("scoped credential = %q, want the bearer token", credential)
	}
	if got := headers.Get("Authorization"); got != "Bearer synthetic-scoped-token" {
		t.Fatalf("scoped authorization header = %q", got)
	}

	// The token bound is inclusive at 16KiB and exclusive above it, exactly like
	// Rust's `token.len() <= 16 * 1024`.
	atLimit := strings.Repeat("a", 16*1024)
	if _, credential, err := resolveRemoteEnvironmentAuth(context.Background(),
		registryCredentialConfig(baseURL, "env-1", bearerHeaders(atLimit)).withAuthMode(registryAuthModeExecutorCredential)); err != nil {
		t.Fatalf("resolve at the 16KiB bound error = %v, want nil", err)
	} else if credential != atLimit {
		t.Fatalf("resolve at the 16KiB bound = %d bytes, want the token", len(credential))
	}
	overLimit := strings.Repeat("a", 16*1024+1)
	if _, _, err := resolveRemoteEnvironmentAuth(context.Background(),
		registryCredentialConfig(baseURL, "env-1", bearerHeaders(overLimit)).withAuthMode(registryAuthModeExecutorCredential)); err == nil ||
		!strings.Contains(err.Error(), "invalid executor credential authorization") {
		t.Fatalf("resolve above the 16KiB bound error = %v, want the invalid credential error", err)
	}
}

// Rust #54346 (`invalid_executor_credential_headers_are_never_sent`): every
// malformed scoped header set is rejected locally, so the executor never sends a
// half-valid credential to the registry.
func TestInvalidExecutorCredentialHeadersAreNeverSentLikeRust(t *testing.T) {
	var registryHits int32
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&registryHits, 1)
		_, _ = w.Write([]byte(registryCredentialRegistrationBody("env-1", "")))
	}))
	defer registry.Close()

	tooLong := strings.Repeat("a", 16*1024+1)
	cases := map[string]http.Header{
		"no_headers":          {},
		"empty_bearer":        {"Authorization": {"Bearer "}},
		"basic_scheme":        {"Authorization": {"Basic synthetic-secret"}},
		"non_ascii_token":     {"Authorization": {"Bearer synthetic-\u00ff-secret"}},
		"whitespace_in_token": {"Authorization": {"Bearer two words"}},
		"token_too_long":      {"Authorization": {"Bearer " + tooLong}},
		"two_values":          {"Authorization": {"Bearer a", "Bearer b"}},
		"extra_header":        {"Authorization": {"Bearer a"}, "X-Account": {"account"}},
		// A Go http.Header literal keeps both Authorization spellings as two
		// entries (only Set/Add canonicalize), so this case depends on the
		// "exactly one header" gate alone, in any map iteration order. Rust's
		// normalized HeaderMap collapses the spellings into one entry, which the
		// sibling two_values case covers there.
		"two_authorization_spellings": {"Authorization": {"Bearer a"}, "authorization": {"Bearer b"}},
		// A lone header that is not Authorization at all.
		"non_authorization_header": {"Content-Type": {"application/json"}},
		"wrong_header_name":        {"X-Authorization": {"Bearer a"}},
		"lowercase_bearer":         {"Authorization": {"bearer a"}},
		"trailing_space":           {"Authorization": {"Bearer a "}},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			scoped, err := registryCredentialConfig(registry.URL, "env-1", headers).WithExecutorCredentialAuth()
			if err != nil {
				t.Fatalf("WithExecutorCredentialAuth() error = %v", err)
			}
			if _, _, err := resolveRemoteEnvironmentAuth(context.Background(), scoped); err == nil ||
				!strings.Contains(err.Error(), "invalid executor credential authorization") {
				t.Fatalf("resolveRemoteEnvironmentAuth() error = %v, want the invalid credential error", err)
			}
			if _, err := registerRemoteEnvironment(context.Background(), scoped, RemotePublicKey{}); err == nil {
				t.Fatal("registerRemoteEnvironment() = nil, want the invalid credential error")
			}
		})
	}
	if got := atomic.LoadInt32(&registryHits); got != 0 {
		t.Fatalf("registry received %d requests, want 0: an invalid scoped credential must never be sent", got)
	}
}

// TestExecutorCredentialHeaderCountGateLikeRust pins the two header-shape gates
// of Rust #54346's scoped `resolve_auth` on their own -- `headers.len() == 1`
// (the count gate) and the normalized `headers.get(AUTHORIZATION)` lookup (the
// name gate). The companion test above drives the whole validation chain, so a
// case that a later gate (token syntax, empty/oversized token, bearer prefix)
// would reject anyway cannot tell whether these two still work. Every case
// below carries a well-formed `Bearer` token, so only the gate named in its
// comment can reject it, and the cases with two entries are rejected whichever
// spelling the Go map yields last.
func TestExecutorCredentialHeaderCountGateLikeRust(t *testing.T) {
	authorization := func(value string) http.Header { return http.Header{"Authorization": {value}} }
	for _, testCase := range []struct {
		name    string
		headers http.Header
	}{
		{
			// Two Authorization-equal-fold keys with one value each: whichever
			// entry the map yields last, only the header-count gate rejects it.
			name:    "two_authorization_spellings",
			headers: http.Header{"Authorization": {"Bearer a"}, "authorization": {"Bearer b"}},
		},
		{
			name:    "authorization_plus_unrelated_header",
			headers: http.Header{"Authorization": {"Bearer a"}, "X-Account": {"account"}},
		},
		{
			name:    "two_values_on_one_header",
			headers: http.Header{"Authorization": {"Bearer a", "Bearer b"}},
		},
		{
			// A single header whose name is not Authorization, carrying a
			// well-formed bearer: only the header-name gate rejects it (a
			// count-only gate would let it through and the token would reach
			// the registry).
			name:    "non_authorization_header_only",
			headers: http.Header{"Content-Type": {"Bearer a"}},
		},
		{
			name:    "no_headers",
			headers: http.Header{},
		},
		{
			// Control: the same shape the scoped mode accepts.
			name:    "single_bearer_is_accepted",
			headers: authorization("Bearer scoped-token"),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			token, err := executorCredentialFromHeaders(testCase.headers)
			wantAccepted := testCase.name == "single_bearer_is_accepted"
			if wantAccepted {
				if err != nil || token != "scoped-token" {
					t.Fatalf("executorCredentialFromHeaders(%#v) = (%q, %v), want the scoped token", testCase.headers, token, err)
				}
				return
			}
			if !errors.Is(err, errInvalidExecutorCredentialAuthorization) {
				t.Fatalf("executorCredentialFromHeaders(%#v) error = %v, want %v", testCase.headers, err, errInvalidExecutorCredentialAuthorization)
			}
		})
	}

	// The rejected headers must also never reach the registry.
	var registryHits int32
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&registryHits, 1)
		_, _ = w.Write([]byte(registryCredentialRegistrationBody("env-1", "")))
	}))
	defer registry.Close()
	for _, headers := range []http.Header{
		{"Authorization": {"Bearer a"}, "authorization": {"Bearer b"}},
		{"Content-Type": {"Bearer a"}},
	} {
		scoped, err := registryCredentialConfig(registry.URL, "env-1", headers).WithExecutorCredentialAuth()
		if err != nil {
			t.Fatalf("WithExecutorCredentialAuth() error = %v", err)
		}
		if _, err := registerRemoteEnvironment(context.Background(), scoped, RemotePublicKey{}); err == nil {
			t.Fatal("registerRemoteEnvironment() = nil, want the invalid credential error")
		}
	}
	if got := atomic.LoadInt32(&registryHits); got != 0 {
		t.Fatalf("registry received %d requests, want 0", got)
	}
}

// Rust #54346 (`resolve_auth` calls `require_tls_or_loopback(base_url, "https")`
// and `executor_credentials_require_https_or_loopback`): HTTPS always works,
// plain HTTP is only allowed for a loopback destination.
func TestExecutorCredentialsRequireHTTPSOrLoopbackLikeRust(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://registry.example.test", true},
		{"https://localhost:8443", true},
		{"http://localhost:8080", true},
		{"http://127.0.0.1:8080", true},
		{"http://[::1]:8080", true},
		{"http://LOCALHOST:8080", true},
		{"http://registry.example.test", false},
		{"http://127.0.0.2:8080", true},
		{"http://localhost.example.test", false},
		{"ws://localhost:8080", false},
		{"registry.example.test", false},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			_, err := registryCredentialConfig(tc.url, "env-1", bearerHeaders("token")).WithExecutorCredentialAuth()
			if tc.want && err != nil {
				t.Fatalf("WithExecutorCredentialAuth(%q) error = %v, want nil", tc.url, err)
			}
			if !tc.want {
				if err == nil {
					t.Fatalf("WithExecutorCredentialAuth(%q) = nil, want https-or-loopback error", tc.url)
				}
				if !strings.Contains(err.Error(), "requires https for non-loopback endpoints") {
					t.Fatalf("WithExecutorCredentialAuth(%q) error = %v, want the transport requirement", tc.url, err)
				}
			}
			// A URL the constructor already rejected must also be rejected by the
			// per-request resolution, which Rust repeats inside `resolve_auth`.
			scoped := registryCredentialConfig(tc.url, "env-1", bearerHeaders("token"))
			scoped.authMode = registryAuthModeExecutorCredential
			resolveErr := func() error {
				_, _, err := resolveRemoteEnvironmentAuth(context.Background(), scoped)
				return err
			}()
			if tc.want != (resolveErr == nil) {
				t.Fatalf("resolveRemoteEnvironmentAuth(%q) error = %v, want allowed=%v", tc.url, resolveErr, tc.want)
			}
		})
	}
}

// Rust #54346 (`scoped_loopback_registry_bypasses_a_configured_proxy`): a system
// proxy would receive a loopback HTTP credential in plaintext, so the scoped
// request uses the proxy-bypassing client (`http_direct`) while the legacy mode
// still honours the proxy.
func TestScopedLoopbackRegistryBypassesAConfiguredProxyLikeRust(t *testing.T) {
	const credential = "synthetic-scoped-token"
	var proxyHits, registryHits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&proxyHits, 1)
		_, _ = w.Write([]byte(registryCredentialRegistrationBody("env-1", "")))
	}))
	defer proxy.Close()
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&registryHits, 1)
		_, _ = w.Write([]byte(registryCredentialRegistrationBody("env-1", "")))
	}))
	defer registry.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	proxiedClient := func() *http.Client {
		return &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := registryCredentialConfig(registry.URL, "env-1", bearerHeaders(credential))
	cfg.HTTPClient = proxiedClient()
	scoped, err := cfg.WithExecutorCredentialAuth()
	if err != nil {
		t.Fatalf("WithExecutorCredentialAuth() error = %v", err)
	}
	authClient, err := remoteRegistryHTTPClientForAuth(scoped)
	if err != nil {
		t.Fatalf("remoteRegistryHTTPClientForAuth() error = %v", err)
	}
	transport, ok := authClient.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("scoped loopback HTTP client transport = %#v, want a proxy-free *http.Transport", authClient.Transport)
	}
	if _, err := registerRemoteEnvironment(ctx, scoped, RemotePublicKey{}); err != nil {
		t.Fatalf("scoped registerRemoteEnvironment() error = %v", err)
	}
	if got := atomic.LoadInt32(&proxyHits); got != 0 {
		t.Fatalf("proxy received %d scoped registry requests, want 0 (the credential must not reach a proxy)", got)
	}
	if got := atomic.LoadInt32(&registryHits); got != 1 {
		t.Fatalf("registry received %d scoped registry requests, want 1", got)
	}

	// The legacy mode keeps using the configured client, which honours the proxy.
	legacy := cfg
	legacy.HTTPClient = proxiedClient()
	if _, err := registerRemoteEnvironment(ctx, legacy, RemotePublicKey{}); err != nil {
		t.Fatalf("legacy registerRemoteEnvironment() error = %v", err)
	}
	if got := atomic.LoadInt32(&proxyHits); got != 1 {
		t.Fatalf("proxy received %d requests after the legacy registration, want 1 (legacy mode must keep honouring the proxy)", got)
	}
	if got := atomic.LoadInt32(&registryHits); got != 1 {
		t.Fatalf("registry received %d requests after the legacy registration, want it to stay at 1", got)
	}
}

// Rust #54346 (`executor_credential_is_never_returned_in_registry_diagnostics`):
// a registry response that echoes the credential anywhere is rejected with fixed
// text, and the retry-relevant codes survive without carrying registry prose.
func TestExecutorCredentialIsNeverEchoedInRegistryDiagnosticsLikeRust(t *testing.T) {
	const credential = "synthetic-scoped-registry-secret"
	const envID = "env-1"
	cases := []struct {
		name      string
		status    int
		body      func() string
		wantCode  string
		wantRetry bool
	}{
		{
			name:   "echoed_in_value",
			status: http.StatusOK,
			body: func() string {
				return registryCredentialRegistrationBody(envID, credential)
			},
		},
		{
			name:   "echoed_in_key",
			status: http.StatusOK,
			body: func() string {
				encoded, err := json.Marshal(map[string]any{credential: "1"})
				if err != nil {
					t.Fatal(err)
				}
				return string(encoded)
			},
		},
		{
			name:   "conflict_body_echoes_credential",
			status: http.StatusServiceUnavailable,
			body: func() string {
				return fmt.Sprintf(`{"error":{"code":"registration_conflict","message":"conflict for %s"}}`, credential)
			},
			wantCode:  "registration_conflict",
			wantRetry: true,
		},
		{
			name:   "unavailable_body_echoes_credential",
			status: http.StatusBadGateway,
			body: func() string {
				return fmt.Sprintf(`{"error":{"code":"authentication_service_unavailable","message":"unavailable for %s"}}`, credential)
			},
			wantCode:  "authentication_service_unavailable",
			wantRetry: true,
		},
		{
			name:   "unknown_code_is_dropped",
			status: http.StatusInternalServerError,
			body: func() string {
				return fmt.Sprintf(`{"error":{"code":"registry_internal","message":"boom %s"}}`, credential)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body()))
			}))
			defer registry.Close()

			cfg := registryCredentialConfig(registry.URL, envID, bearerHeaders(credential))
			scoped, err := cfg.WithExecutorCredentialAuth()
			if err != nil {
				t.Fatalf("WithExecutorCredentialAuth() error = %v", err)
			}
			_, err = registerRemoteEnvironment(context.Background(), scoped, RemotePublicKey{})
			if err == nil {
				t.Fatalf("registerRemoteEnvironment() = nil, want an error")
			}
			if strings.Contains(err.Error(), credential) {
				t.Fatalf("registry error leaked the credential: %v", err)
			}
			var httpErr *remoteRegistryHTTPError
			if errors.As(err, &httpErr) {
				gotCode := ""
				if httpErr.Code != nil {
					gotCode = *httpErr.Code
				}
				if gotCode != tc.wantCode {
					t.Fatalf("registry error code = %q, want %q", gotCode, tc.wantCode)
				}
				if retryable := retryableRegistryRegistrationError(httpErr); retryable != tc.wantRetry {
					t.Fatalf("retryableRegistryRegistrationError() = %v, want %v", retryable, tc.wantRetry)
				}
				return
			}
			if tc.wantCode != "" {
				t.Fatalf("registry error = %v (%T), want an *remoteRegistryHTTPError carrying %q", err, err, tc.wantCode)
			}
			if !strings.Contains(err.Error(), "invalid environment registry executor registration response") {
				t.Fatalf("registry error = %v, want the fixed echo-rejection error", err)
			}
		})
	}
}

// Rust #54346: the unauthorized path does not even read the body, so a body that
// echoes the credential cannot leak through the auth diagnostic.
func TestExecutorCredentialUnauthorizedBodyIsOmittedLikeRust(t *testing.T) {
	const credential = "synthetic-scoped-registry-secret"
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"message":"denied for %s"}}`, credential)))
	}))
	defer registry.Close()

	cfg := registryCredentialConfig(registry.URL, "env-1", bearerHeaders(credential))
	scoped, err := cfg.WithExecutorCredentialAuth()
	if err != nil {
		t.Fatalf("WithExecutorCredentialAuth() error = %v", err)
	}
	_, err = registerRemoteEnvironment(context.Background(), scoped, RemotePublicKey{})
	if err == nil {
		t.Fatal("registerRemoteEnvironment() = nil, want the authentication error")
	}
	if strings.Contains(err.Error(), credential) {
		t.Fatalf("registry error leaked the credential: %v", err)
	}
	if !strings.Contains(err.Error(), "response body omitted") {
		t.Fatalf("registry error = %v, want the omitted-body diagnostic", err)
	}
}

// Rust #54346: the legacy mode is untouched. The same body shape that the scoped
// mode rejects is still accepted, exactly like the pre-#54346 code.
func TestLegacyRegistryAuthKeepsAccountCredentialsLikeRust(t *testing.T) {
	const envID = "env-1"
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer registry-token" {
			t.Errorf("Authorization = %q, want the account credential", got)
		}
		_, _ = w.Write([]byte(registryCredentialRegistrationBody(envID, "")))
	}))
	defer registry.Close()

	cfg := registryCredentialConfig(registry.URL, envID, bearerHeaders("registry-token"))
	registration, err := registerRemoteEnvironment(context.Background(), cfg, RemotePublicKey{})
	if err != nil {
		t.Fatalf("legacy registerRemoteEnvironment() error = %v", err)
	}
	if registration == nil || registration.EnvironmentID != envID {
		t.Fatalf("legacy registration = %#v", registration)
	}

	// The scoped gate and the legacy gate accept the same single bearer, so the
	// difference is the URL rule, not the header shape.
	if _, _, err := resolveRemoteEnvironmentAuth(context.Background(), cfg); err != nil {
		t.Fatalf("legacy resolve error = %v", err)
	}
	scoped, err := cfg.WithExecutorCredentialAuth()
	if err != nil {
		t.Fatalf("WithExecutorCredentialAuth(loopback) error = %v", err)
	}
	registration, err = registerRemoteEnvironment(context.Background(), scoped, RemotePublicKey{})
	if err != nil {
		t.Fatalf("scoped registerRemoteEnvironment() error = %v", err)
	}
	if registration == nil || registration.EnvironmentID != envID {
		t.Fatalf("scoped registration = %#v", registration)
	}
}

// Rust #54346 (`require_tls_or_loopback`, direct.rs:297): the helper's own
// contract, including the insecure-scheme arm.
func TestRequireSecureOrLoopbackLikeRust(t *testing.T) {
	cases := []struct {
		url     string
		scheme  string
		wantErr bool
	}{
		{"https://registry.example.test", "https", false},
		{"http://localhost:8080", "https", false},
		{"http://[::1]:8080", "https", false},
		{"http://127.0.0.1:8080", "https", false},
		{"http://registry.example.test", "https", true},
		{"wss://registry.example.test", "wss", false},
		{"ws://localhost:8080", "wss", false},
		{"ws://registry.example.test", "wss", true},
		{"https://registry.example.test", "wss", true},
		{"://broken", "https", true},
	}
	for _, tc := range cases {
		err := requireSecureOrLoopback(tc.url, tc.scheme)
		if tc.wantErr != (err != nil) {
			t.Fatalf("requireSecureOrLoopback(%q, %q) error = %v, wantErr %v", tc.url, tc.scheme, err, tc.wantErr)
		}
	}
}

// Rust #54346: the JSON echo scan looks at every string value and every key,
// recursively, and never reports a match for an empty credential.
func TestJSONContainsExecutorCredentialLikeRust(t *testing.T) {
	const credential = "secret-token"
	document := map[string]any{
		"a": []any{"x", map[string]any{"nested": "value with secret-token inside"}},
		"b": 42.0,
		"c": nil,
		"d": true,
	}
	if !jsonContainsExecutorCredential(document, credential) {
		t.Fatal("jsonContainsExecutorCredential() = false, want the nested echo to be found")
	}
	if jsonContainsExecutorCredential(map[string]any{"a": "clean"}, credential) {
		t.Fatal("jsonContainsExecutorCredential() = true for a clean document")
	}
	if jsonContainsExecutorCredential(map[string]any{"a": credential}, "") {
		t.Fatal("jsonContainsExecutorCredential() = true for an empty credential")
	}
	if !jsonContainsExecutorCredential(map[string]any{"pre-secret-token": "1"}, credential) {
		t.Fatal("jsonContainsExecutorCredential() = false, want an echoed object key to be found")
	}
}
