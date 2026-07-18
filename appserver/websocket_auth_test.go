package appserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebSocketCapabilityTokenAuthorizesMatchingBearer(t *testing.T) {
	digest := sha256.Sum256([]byte("secret-token"))
	policy, err := NewWebSocketAuthPolicy(&WebSocketAuthSettings{
		Mode:        WebSocketAuthCapabilityToken,
		TokenSHA256: hex.EncodeToString(digest[:]),
	})
	if err != nil {
		t.Fatalf("NewWebSocketAuthPolicy error = %v", err)
	}
	if err := policy.Authorize(http.Header{"Authorization": []string{"Bearer secret-token"}}, time.Now()); err != nil {
		t.Fatalf("Authorize matching token error = %v", err)
	}
	err = policy.Authorize(http.Header{"Authorization": []string{"Bearer wrong-token"}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "invalid websocket bearer token") {
		t.Fatalf("Authorize wrong token error = %v", err)
	}
}

func TestWebSocketCapabilityTokenReadsTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("file-token\n"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	policy, err := NewWebSocketAuthPolicy(&WebSocketAuthSettings{Mode: WebSocketAuthCapabilityToken, TokenFile: path})
	if err != nil {
		t.Fatalf("NewWebSocketAuthPolicy error = %v", err)
	}
	if err := policy.Authorize(http.Header{"Authorization": []string{"Bearer file-token"}}, time.Now()); err != nil {
		t.Fatalf("Authorize file token error = %v", err)
	}
}

func TestWebSocketSignedBearerTokenValidatesClaims(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "secret")
	secret := "01234567890123456789012345678901"
	if err := os.WriteFile(secretPath, []byte(secret), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	policy, err := NewWebSocketAuthPolicy(&WebSocketAuthSettings{
		Mode:             WebSocketAuthSignedBearerToken,
		SharedSecretFile: secretPath,
		Issuer:           "issuer-a",
		Audience:         "audience-a",
	})
	if err != nil {
		t.Fatalf("NewWebSocketAuthPolicy error = %v", err)
	}
	now := time.Unix(1000, 0)
	token := signedWebSocketTokenForTest([]byte(secret), map[string]any{
		"exp": now.Add(time.Minute).Unix(),
		"nbf": now.Add(-time.Minute).Unix(),
		"iss": "issuer-a",
		"aud": []string{"audience-a", "audience-b"},
	})
	if err := policy.Authorize(http.Header{"Authorization": []string{"Bearer " + token}}, now); err != nil {
		t.Fatalf("Authorize signed token error = %v", err)
	}
	expired := signedWebSocketTokenForTest([]byte(secret), map[string]any{
		"exp": now.Add(-time.Hour).Unix(),
		"iss": "issuer-a",
		"aud": "audience-a",
	})
	err = policy.Authorize(http.Header{"Authorization": []string{"Bearer " + expired}}, now)
	if err == nil || !strings.Contains(err.Error(), "expired websocket jwt") {
		t.Fatalf("Authorize expired token error = %v", err)
	}
}

func TestWebSocketAuthRejectsShortSignedSecret(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("too-short"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	_, err := NewWebSocketAuthPolicy(&WebSocketAuthSettings{
		Mode:             WebSocketAuthSignedBearerToken,
		SharedSecretFile: secretPath,
	})
	if err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("NewWebSocketAuthPolicy error = %v", err)
	}
}

func TestWebSocketAuthDetectsNonLoopbackWithoutPolicy(t *testing.T) {
	policy, err := NewWebSocketAuthPolicy(nil)
	if err != nil {
		t.Fatalf("NewWebSocketAuthPolicy error = %v", err)
	}
	if !policy.IsUnauthenticatedNonLoopbackListener(&net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 8765}) {
		t.Fatal("non-loopback listener should require auth")
	}
	if policy.IsUnauthenticatedNonLoopbackListener(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8765}) {
		t.Fatal("loopback listener should not require auth")
	}
}

func signedWebSocketTokenForTest(secret []byte, claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadData, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadData)
	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signature
}
