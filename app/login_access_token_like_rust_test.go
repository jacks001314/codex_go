package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func encodeUnsignedAgentIdentityJWT(t *testing.T, kid string, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "RS256", "kid": kid})
	if err != nil {
		t.Fatalf("Marshal(header) error = %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal(claims) error = %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("not-a-real-signature"))
}

func agentIdentityClaims() map[string]any {
	return map[string]any{
		"iss":                        "https://chatgpt.com/codex-backend/agent-identity",
		"aud":                        "codex-app-server",
		"iat":                        time.Now().Add(-time.Hour).Unix(),
		"exp":                        time.Now().Add(time.Hour).Unix(),
		"agent_runtime_id":           "runtime-1",
		"agent_private_key":          "private-key",
		"account_id":                 "account-1",
		"chatgpt_user_id":            "user-1",
		"plan_type":                  "pro",
		"chatgpt_account_is_fedramp": false,
	}
}

// TestLoginWithAccessTokenRejectsMalformedJWTLikeRust pins Rust's ordering for
// `codex login --with-access-token`: the token is decoded as an agent identity
// JWT before anything is stored, so a malformed token fails without writing
// auth.json.
func TestLoginWithAccessTokenRejectsMalformedJWTLikeRust(t *testing.T) {
	for _, token := range []string{"not-a-jwt", "a.b.c"} {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		t.Setenv("OPENAI_API_KEY", "")
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"login", "--with-access-token"}, strings.NewReader(token+"\n"), &stdout, &stderr)
		var exitErr *ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != 1 {
			t.Fatalf("token %q: error = %v, want exit status 1", token, err)
		}
		if !strings.Contains(stderr.String(), accessTokenStdinReadingMessage) {
			t.Fatalf("token %q: stderr = %q, missing the stdin prompt", token, stderr.String())
		}
		if !strings.HasPrefix(err.Error(), "Error logging in with access token: ") {
			t.Fatalf("token %q: error = %q", token, err.Error())
		}
		if _, statErr := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(statErr) {
			t.Fatalf("token %q: auth.json written, stat error = %v", token, statErr)
		}
	}
}

// TestLoginWithAccessTokenVerifiesSignatureLikeRust pins verified_record_from_jwt:
// a well-formed token still needs a JWKS key with a matching kid before it is
// accepted, so login fails instead of persisting an unverified credential.
func TestLoginWithAccessTokenVerifiesSignatureLikeRust(t *testing.T) {
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[{"kid":"other-key","kty":"RSA","alg":"RS256","n":"AQAB","e":"AQAB"}]}`))
	}))
	defer jwks.Close()

	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv(agentIdentityJWKSBaseURLEnvVar, jwks.URL)
	token := encodeUnsignedAgentIdentityJWT(t, "test-key", agentIdentityClaims())
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"login", "--with-access-token"}, strings.NewReader(token+"\n"), &stdout, &stderr)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("error = %v, stderr = %q, want exit status 1", err, stderr.String())
	}
	if !strings.Contains(err.Error(), "agent identity JWT kid test-key is not trusted") {
		t.Fatalf("error = %q", err.Error())
	}
	if _, statErr := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(statErr) {
		t.Fatalf("auth.json written, stat error = %v", statErr)
	}
}
