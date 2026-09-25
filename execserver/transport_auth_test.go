package execserver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/websocketauth"
)

// startAuthenticatedExecServerForTest serves the exec-server transport with the
// given listener policy and returns its URL.
func startAuthenticatedExecServerForTest(t *testing.T, policy *websocketauth.Policy) string {
	t.Helper()
	serverCtx, cancelServer := context.WithCancel(context.Background())
	t.Cleanup(cancelServer)
	urlCh := make(chan string, 1)
	server := NewServer()
	server.SetWebSocketAuthPolicy(policy)
	go func() {
		_ = server.ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	select {
	case url := <-urlCh:
		return url
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server URL was not reported")
		return ""
	}
}

func capabilityTokenPolicy(t *testing.T, token string) *websocketauth.Policy {
	t.Helper()
	policy, err := websocketauth.NewPolicy(&websocketauth.Settings{
		Mode:        websocketauth.ModeCapabilityToken,
		TokenSHA256: hex.EncodeToString(sha256Sum(token)),
	})
	if err != nil {
		t.Fatalf("NewPolicy() error = %v", err)
	}
	return policy
}

func sha256Sum(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

// signedJWTForTest builds an HS256 JWT with the shared secret.
func signedJWTForTest(t *testing.T, secret string, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Rust parity: codex-rs/cli/tests/exec_server_websocket_auth.rs (#47601): with
// listener authentication configured, every WebSocket upgrade must present a
// valid `Authorization: Bearer` credential and is otherwise rejected with 401;
// unauthenticated listeners keep working.
func TestExecServerListenerWebSocketAuthLikeRust(t *testing.T) {
	const token = "executor-capability-token"
	authenticatedURL := startAuthenticatedExecServerForTest(t, capabilityTokenPolicy(t, token))

	// Missing credentials: the upgrade is rejected with 401.
	if _, err := DialClient(context.Background(), authenticatedURL, "exec-server-auth-test"); err == nil {
		t.Fatal("DialClient() without a token succeeded against an authenticated listener")
	} else if !strings.Contains(err.Error(), "401") {
		t.Fatalf("unauthenticated dial error = %v, want HTTP 401", err)
	}

	// Wrong token: rejected too.
	if _, err := DialClientWithOptions(context.Background(), authenticatedURL, DialClientOptions{
		ClientName:  "exec-server-auth-test",
		HTTPHeaders: http.Header{"Authorization": []string{"Bearer wrong-token"}},
	}); err == nil {
		t.Fatal("DialClient() with a wrong token succeeded")
	}

	// Correct token: the RPC initialization succeeds.
	client, err := DialClientWithOptions(context.Background(), authenticatedURL, DialClientOptions{
		ClientName:  "exec-server-auth-test",
		HTTPHeaders: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		t.Fatalf("authenticated DialClientWithOptions() error = %v", err)
	}
	if client.SessionID() == "" {
		t.Fatal("authenticated client session id is empty")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// An unauthenticated listener still accepts token-free connections.
	openURL := startAuthenticatedExecServerForTest(t, nil)
	openClient, err := DialClient(context.Background(), openURL, "exec-server-open-test")
	if err != nil {
		t.Fatalf("unauthenticated DialClient() error = %v", err)
	}
	_ = openClient.Close()
}

// TestExecServerListenerSignedBearerTokenAuthLikeRust pins the signed-JWT mode:
// a valid HS256 token with the expected issuer and audience is accepted, an
// expired one is not.
func TestExecServerListenerSignedBearerTokenAuthLikeRust(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	secretFile := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secretFile, []byte(secret), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	policy, err := websocketauth.NewPolicy(&websocketauth.Settings{
		Mode:             websocketauth.ModeSignedBearerToken,
		SharedSecretFile: secretFile,
		Issuer:           "exec-server",
		Audience:         "executor",
	})
	if err != nil {
		t.Fatalf("NewPolicy() error = %v", err)
	}
	serverURL := startAuthenticatedExecServerForTest(t, policy)

	valid := signedJWTForTest(t, secret, map[string]any{
		"exp": time.Now().Add(time.Minute).Unix(),
		"iss": "exec-server",
		"aud": "executor",
	})
	client, err := DialClientWithOptions(context.Background(), serverURL, DialClientOptions{
		ClientName:  "exec-server-jwt-test",
		HTTPHeaders: http.Header{"Authorization": []string{"Bearer " + valid}},
	})
	if err != nil {
		t.Fatalf("signed bearer dial error = %v", err)
	}
	_ = client.Close()

	expired := signedJWTForTest(t, secret, map[string]any{
		"exp": time.Now().Add(-2 * time.Minute).Unix(),
		"iss": "exec-server",
		"aud": "executor",
	})
	if _, err := DialClientWithOptions(context.Background(), serverURL, DialClientOptions{
		ClientName:  "exec-server-jwt-test",
		HTTPHeaders: http.Header{"Authorization": []string{"Bearer " + expired}},
	}); err == nil {
		t.Fatal("expired signed bearer token was accepted")
	}

	wrongAudience := signedJWTForTest(t, secret, map[string]any{
		"exp": time.Now().Add(time.Minute).Unix(),
		"iss": "exec-server",
		"aud": "other",
	})
	if _, err := DialClientWithOptions(context.Background(), serverURL, DialClientOptions{
		ClientName:  "exec-server-jwt-test",
		HTTPHeaders: http.Header{"Authorization": []string{"Bearer " + wrongAudience}},
	}); err == nil {
		t.Fatal("signed bearer token with the wrong audience was accepted")
	}
}
