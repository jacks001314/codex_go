package auth

import (
	"errors"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Mirrors Rust oauth::pkce: a 64-byte URL-safe verifier and its S256 challenge.
func TestGeneratePKCELikeRust(t *testing.T) {
	pkce, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE() error = %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(pkce.codeVerifier)
	if err != nil || len(raw) != 64 {
		t.Fatalf("verifier decoded %d bytes (%v), want 64 random bytes", len(raw), err)
	}
	digest := sha256.Sum256([]byte(pkce.codeVerifier))
	if want := base64.RawURLEncoding.EncodeToString(digest[:]); pkce.codeChallenge != want {
		t.Fatalf("challenge = %q, want S256 %q", pkce.codeChallenge, want)
	}
}

// Mirrors Rust oauth::authorization::build_authorization_url parameter order.
func TestBuildAuthorizationURLLikeRust(t *testing.T) {
	pkce := &pkceCodes{codeVerifier: "verifier", codeChallenge: "challenge"}
	target, err := buildAuthorizationURL(authorizationRequest{
		endpoint:        "https://issuer.example.com/oauth/authorize?audience=existing",
		clientID:        "client-1",
		redirectURI:     "http://127.0.0.1:1455/callback",
		scope:           "openid profile",
		resource:        "https://api.example.com",
		pkce:            pkce,
		state:           "state-1",
		extraParameters: [][2]string{{"prompt", "consent"}},
	})
	if err != nil {
		t.Fatalf("buildAuthorizationURL() error = %v", err)
	}
	want := "https://issuer.example.com/oauth/authorize?audience=existing" +
		"&response_type=code&client_id=client-1&redirect_uri=http%3A%2F%2F127.0.0.1%3A1455%2Fcallback" +
		"&code_challenge=challenge&code_challenge_method=S256&state=state-1" +
		"&scope=openid+profile&resource=https%3A%2F%2Fapi.example.com&prompt=consent"
	if target != want {
		t.Fatalf("authorization URL = %q, want %q", target, want)
	}
	if _, err := buildAuthorizationURL(authorizationRequest{endpoint: "not a url"}); err == nil {
		t.Fatal("an invalid endpoint must be rejected")
	}
}

// Mirrors Rust oauth::authorization::CallbackParameters::validate: state is
// checked before a code or provider error is accepted.
func TestCallbackParametersValidateLikeRust(t *testing.T) {
	parse := func(raw string) callbackParameters {
		t.Helper()
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("url.Parse(%q) error = %v", raw, err)
		}
		return parseCallbackParameters(parsed)
	}
	if code, err := parse("http://127.0.0.1/callback?code=abc&state=s1").validate("s1"); err != nil || code != "abc" {
		t.Fatalf("matching state = %q, %#v", code, err)
	}
	if _, err := parse("http://127.0.0.1/callback?code=abc&state=other").validate("s1"); err == nil || err.kind != callbackErrorStateMismatch {
		t.Fatalf("state mismatch error = %#v", err)
	}
	if _, err := parse("http://127.0.0.1/callback?state=s1&error=access_denied&error_description=no").validate("s1"); err == nil ||
		err.kind != callbackErrorProvider || err.code != "access_denied" || err.description != "no" {
		t.Fatalf("provider error = %#v", err)
	}
	if _, err := parse("http://127.0.0.1/callback?state=s1").validate("s1"); err == nil || err.kind != callbackErrorMissingCode {
		t.Fatalf("missing code error = %#v", err)
	}
	if got := (&callbackError{kind: callbackErrorProvider}).asError(); got == nil || got.Error() != "provider OAuth authorization was denied" {
		t.Fatalf("provider asError() = %v", got)
	}
}

func newTestOAuthClient(t *testing.T, server *httptest.Server, encoding tokenEncoding, limit errorBodyLimit) *oauthClient {
	t.Helper()
	return newOAuthClient(server.Client(), tokenEndpoint{
		url:            server.URL,
		clientID:       "client-1",
		encoding:       encoding,
		timeout:        5 * time.Second,
		errorBodyLimit: limit,
	})
}

// Mirrors Rust oauth::client: form-encoded grants post the standard parameters
// and decode the token response.
func TestOAuthClientExchangeAndRefreshLikeRust(t *testing.T) {
	var lastForm url.Values
	var lastContentType string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		lastContentType = request.Header.Get("Content-Type")
		body, _ := io.ReadAll(request.Body)
		lastForm, _ = url.ParseQuery(string(body))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"access_token":"token-1","token_type":"bearer","expires_in":3600,"refresh_token":"refresh-2"}`))
	}))
	defer server.Close()
	client := newTestOAuthClient(t, server, tokenEncodingForm, errorBodyLimit{bytes: 1024})

	var exchanged map[string]any
	if err := client.exchangeCode(context.Background(), authorizationCodeGrant{
		code:        "code-1",
		redirectURI: "http://127.0.0.1:1455/callback",
		pkce:        &pkceCodes{codeVerifier: "verifier"},
		resource:    "https://api.example.com",
	}, &exchanged); err != nil {
		t.Fatalf("exchangeCode() error = %v", err)
	}
	if lastContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("content type = %q", lastContentType)
	}
	want := map[string]string{
		"grant_type": "authorization_code", "client_id": "client-1", "code": "code-1",
		"redirect_uri": "http://127.0.0.1:1455/callback", "code_verifier": "verifier",
		"resource": "https://api.example.com",
	}
	for key, value := range want {
		if lastForm.Get(key) != value {
			t.Fatalf("form[%s] = %q, want %q (%v)", key, lastForm.Get(key), value, lastForm)
		}
	}
	if exchanged["access_token"] != "token-1" {
		t.Fatalf("decoded response = %#v", exchanged)
	}

	var refreshed map[string]any
	if err := client.refresh(context.Background(), refreshTokenGrant{refreshToken: "refresh-1"}, &refreshed); err != nil {
		t.Fatalf("refresh() error = %v", err)
	}
	if lastForm.Get("grant_type") != "refresh_token" || lastForm.Get("refresh_token") != "refresh-1" || lastForm.Get("resource") != "" {
		t.Fatalf("refresh form = %v", lastForm)
	}
}

// Mirrors Rust oauth::client JSON encoding (ChatGPT refresh) and the opaque
// invalid-response error: decoder errors can contain token values.
func TestOAuthClientJSONEncodingAndInvalidResponseLikeRust(t *testing.T) {
	var body map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if contentType := request.Header.Get("Content-Type"); contentType != "application/json" {
			t.Errorf("content type = %q", contentType)
		}
		decoded, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(decoded, &body)
		_, _ = writer.Write([]byte("not json"))
	}))
	defer server.Close()
	client := newTestOAuthClient(t, server, tokenEncodingJSON, errorBodyLimit{unlimited: true})

	var out map[string]any
	oauthErr := client.refresh(context.Background(), refreshTokenGrant{refreshToken: "refresh-1"}, &out)
	var typed *oauthError
	if !errors.As(oauthErr, &typed) || typed.kind != oauthErrorInvalidResponse {
		t.Fatalf("invalid response error = %#v", oauthErr)
	}
	if oauthErr.Error() != "OAuth token response is invalid" {
		t.Fatalf("invalid response message = %q", oauthErr.Error())
	}
	if body["grant_type"] != "refresh_token" || body["client_id"] != "client-1" || body["refresh_token"] != "refresh-1" {
		t.Fatalf("json body = %v", body)
	}
}

// Mirrors Rust oauth::error: the rejection keeps the status, a redacted request
// id, the parsed code, and redacted diagnostics; oversized bounded bodies are
// omitted entirely.
func TestOAuthClientRejectionLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("x-request-id", "req-secret-value")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":"invalid_grant","error_description":"bad code code-1"}`))
	}))
	defer server.Close()
	client := newTestOAuthClient(t, server, tokenEncodingForm, errorBodyLimit{unlimited: true})

	oauthErr := client.exchangeCode(context.Background(), authorizationCodeGrant{
		code:        "code-1",
		redirectURI: "http://127.0.0.1:1455/callback",
		pkce:        &pkceCodes{codeVerifier: "verifier-1"},
	}, &map[string]any{})
	var typed *oauthError
	if !errors.As(oauthErr, &typed) {
		t.Fatalf("exchangeCode() error = %v", oauthErr)
	}
	rejection := typed.rejected()
	if rejection == nil {
		t.Fatalf("rejection = %#v (error %v)", typed, oauthErr)
	}
	if rejection.statusCode != http.StatusBadRequest || rejection.errorCode != "invalid_grant" {
		t.Fatalf("rejection = %#v", rejection)
	}
	if strings.Contains(rejection.Error(), "code-1") {
		t.Fatalf("rejection leaked the code: %q", rejection.Error())
	}
	if rejection.requestID != "req-secret-value" {
		t.Fatalf("request id = %q", rejection.requestID)
	}
	// The caller-facing message prefers the redacted error_description, matching
	// Rust's TokenErrorDetail::parse.
	if got := oauthErr.Error(); !strings.Contains(got, "token endpoint returned status 400") ||
		!strings.Contains(got, "bad code [REDACTED]") || strings.Contains(got, "code-1") {
		t.Fatalf("error message = %q", got)
	}

	// A bounded reader drops an oversized body instead of truncating an echoed secret.
	oversized := newTokenRejection(http.StatusBadRequest, http.Header{}, strings.Repeat("x", 64), nil)
	if oversized.displayMessage != strings.Repeat("x", 64) {
		t.Fatalf("unbounded detail = %q", oversized.displayMessage)
	}
}

// Mirrors Rust oauth::client transport errors: the URL's credentials and
// sensitive query values are redacted before the message leaves the client.
func TestOAuthTransportErrorRedactionLikeRust(t *testing.T) {
	client := newOAuthClient(&http.Client{}, tokenEndpoint{
		url:      "http://user:pass@127.0.0.1:1/token?access_token=secret&state=keep",
		clientID: "client-1",
		encoding: tokenEncodingForm,
		timeout:  time.Second,
	})
	oauthErr := client.refresh(context.Background(), refreshTokenGrant{refreshToken: "refresh-1"}, &map[string]any{})
	var typed *oauthError
	if !errors.As(oauthErr, &typed) || typed.kind != oauthErrorTransport {
		t.Fatalf("transport error = %#v", oauthErr)
	}
	message := oauthErr.Error()
	if strings.Contains(message, "pass") || strings.Contains(message, "secret") {
		t.Fatalf("transport error leaked credentials: %q", message)
	}
	// `state` is a sensitive query key in Rust's allowlist too, so the safe
	// parameter here is the host and path shape.
	if !strings.Contains(message, "http://127.0.0.1:1/token?") || strings.Contains(message, "state=keep") {
		t.Fatalf("transport error kept the wrong URL shape: %q", message)
	}
	if got := sanitizeURLForLogging("https://issuer.example.com/token?code=abc&state=s1"); strings.Contains(got, "abc") || !strings.Contains(got, "state=%3Credacted%3E") {
		t.Fatalf("sanitizeURLForLogging() = %q", got)
	}
	if got := sanitizeURLForLogging("not a url"); got != "not a url" {
		t.Fatalf("sanitizeURLForLogging(invalid) = %q", got)
	}
}

// Mirrors Rust gateway_auth_callback: the listener accepts exactly one
// /callback request, validates state, and rejects other paths.
func TestGatewayCallbackListenerLikeRust(t *testing.T) {
	listener, err := newGatewayCallbackListener(0, "state-1")
	if err != nil {
		t.Fatalf("newGatewayCallbackListener() error = %v", err)
	}
	if !strings.HasPrefix(listener.redirectURL(), "http://127.0.0.1:") || !strings.HasSuffix(listener.redirectURL(), "/callback") {
		t.Fatalf("redirect URI = %q", listener.redirectURL())
	}

	// A mismatched state is rejected over HTTP without completing the sign-in.
	response, err := http.Get(listener.redirectURL() + "?code=abc&state=wrong")
	if err != nil {
		t.Fatalf("callback request error = %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "state did not match") {
		t.Fatalf("state mismatch response = %d %q", response.StatusCode, body)
	}

	// A provider denial surfaces Rust's permission-denied text.
	go func() {
		response, err := http.Get(listener.redirectURL() + "?state=state-1&error=access_denied")
		if err == nil {
			_, _ = io.ReadAll(response.Body)
			_ = response.Body.Close()
		}
	}()
	if _, err := listener.wait(context.Background()); err == nil || err.Error() != "provider OAuth authorization was denied" {
		t.Fatalf("wait() error = %v", err)
	}
}
