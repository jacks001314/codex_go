package agent

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentFromChatGPTBaseURL(t *testing.T) {
	env, err := EnvironmentFromChatGPTBaseURL("https://chatgpt.com/backend-api/codex")
	if err != nil || env != ChatGPTProduction {
		t.Fatalf("prod env = %s %v", env, err)
	}
	env, err = EnvironmentFromChatGPTBaseURL("https://chatgpt-staging.com/backend-api")
	if err != nil || env != ChatGPTStaging {
		t.Fatalf("staging env = %s %v", env, err)
	}
	if _, err := EnvironmentFromChatGPTBaseURL("http://localhost:8080"); err == nil {
		t.Fatalf("custom urls should be rejected")
	}
}

func TestAuthorizationHeaderForAgentTask(t *testing.T) {
	key, publicKey := testIdentityKey(t)
	now := time.Unix(0, 0).UTC()
	header, err := AuthorizationHeaderForAgentTask(key, "task", now)
	if err != nil {
		t.Fatalf("AuthorizationHeaderForAgentTask() error = %v", err)
	}
	token := strings.TrimPrefix(header, "AgentAssertion ")
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	var envelope AssertionEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if envelope.AgentRuntimeID != "agent" || envelope.TaskID != "task" || envelope.Timestamp != now.Format(time.RFC3339) {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil {
		t.Fatalf("signature base64: %v", err)
	}
	payload := envelope.AgentRuntimeID + ":" + envelope.TaskID + ":" + envelope.Timestamp
	if !ed25519.Verify(publicKey, []byte(payload), signature) {
		t.Fatalf("agent assertion signature did not verify")
	}
}

func TestDecodeJWTPayload(t *testing.T) {
	email := "user@example.com"
	payload, _ := json.Marshal(JWTClaims{Issuer: JWTIssuer, Audience: JWTAudience, AgentRuntimeID: "agent", Email: &email, PlanType: "hc"})
	jwt := "header." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	claims, err := DecodeJWTPayload(jwt)
	if err != nil {
		t.Fatalf("DecodeJWTPayload() error = %v", err)
	}
	if claims.AgentRuntimeID != "agent" || claims.Email == nil || *claims.Email != "user@example.com" || claims.PlanType != "enterprise" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
}

func TestDecodeAgentIdentityJWTVerifiesJWK(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "test-key"})
	payload, _ := json.Marshal(map[string]any{
		"iss":                        JWTIssuer,
		"aud":                        JWTAudience,
		"iat":                        time.Now().Unix(),
		"exp":                        time.Now().Add(time.Hour).Unix(),
		"agent_runtime_id":           "agent-runtime-id",
		"agent_private_key":          "private-key",
		"account_id":                 "account-id",
		"chatgpt_user_id":            "user-id",
		"plan_type":                  "pro",
		"chatgpt_account_is_fedramp": false,
	})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	hash := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15() error = %v", err)
	}
	jwt := unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
	jwks := &JWKSet{Keys: []JWK{jwkFromRSAPublicKey("test-key", &privateKey.PublicKey)}}

	claims, err := DecodeAgentIdentityJWT(jwt, jwks)
	if err != nil {
		t.Fatalf("DecodeAgentIdentityJWT() error = %v", err)
	}
	if claims.AgentRuntimeID != "agent-runtime-id" || claims.AccountID != "account-id" || claims.PlanType != "pro" {
		t.Fatalf("claims = %#v", claims)
	}
}

func TestDecodeAgentIdentityJWTRejectsUntrustedKID(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "test-key"})
	payload, _ := json.Marshal(map[string]any{
		"iss":                        JWTIssuer,
		"aud":                        JWTAudience,
		"exp":                        time.Now().Add(time.Hour).Unix(),
		"agent_runtime_id":           "agent-runtime-id",
		"agent_private_key":          "private-key",
		"account_id":                 "account-id",
		"chatgpt_user_id":            "user-id",
		"plan_type":                  "pro",
		"chatgpt_account_is_fedramp": false,
	})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	hash := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15() error = %v", err)
	}
	jwt := unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
	jwks := &JWKSet{Keys: []JWK{jwkFromRSAPublicKey("other-key", &privateKey.PublicKey)}}

	if _, err := DecodeAgentIdentityJWT(jwt, jwks); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("DecodeAgentIdentityJWT() error = %v, want untrusted kid", err)
	}
}

func TestAgentIdentityRegistrationClients(t *testing.T) {
	key, _ := testIdentityKey(t)
	material := &GeneratedKeyMaterial{PublicKeySSH: "ssh-ed25519 AAAA", PrivateKeyPKCS8Base64: key.PrivateKeyPKCS8Base64}
	var agentRequest map[string]any
	var taskRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agent/register":
			if r.Header.Get("Authorization") != "Bearer access-token" {
				t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("X-OpenAI-Fedramp") != "true" {
				t.Fatalf("fedramp header = %q", r.Header.Get("X-OpenAI-Fedramp"))
			}
			if err := json.NewDecoder(r.Body).Decode(&agentRequest); err != nil {
				t.Fatalf("decode agent request: %v", err)
			}
			writeJSONAgent(t, w, map[string]string{"agent_runtime_id": "agent-runtime"})
		case "/v1/agent/agent/task/register":
			if err := json.NewDecoder(r.Body).Decode(&taskRequest); err != nil {
				t.Fatalf("decode task request: %v", err)
			}
			writeJSONAgent(t, w, map[string]string{"taskId": "task-runtime"})
		default:
			t.Fatalf("unexpected path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	agentRuntimeID, err := RegisterAgentIdentity(context.Background(), server.Client(), server.URL, "access-token", true, material, &BillOfMaterials{AgentVersion: "1.0.0"}, []string{"code"})
	if err != nil {
		t.Fatalf("RegisterAgentIdentity() error = %v", err)
	}
	if agentRuntimeID != "agent-runtime" || agentRequest["agent_public_key"] != "ssh-ed25519 AAAA" {
		t.Fatalf("agent response/request = %q %#v", agentRuntimeID, agentRequest)
	}
	taskID, err := RegisterAgentTask(context.Background(), server.Client(), server.URL, key, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("RegisterAgentTask() error = %v", err)
	}
	if taskID != "task-runtime" || taskRequest["timestamp"] != "1970-01-01T00:00:00Z" || taskRequest["signature"] == "" {
		t.Fatalf("task response/request = %q %#v", taskID, taskRequest)
	}
}

func TestFetchAgentIdentityJWKS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent-identities/jwks" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		writeJSONAgent(t, w, JWKSet{Keys: []JWK{{KID: "kid", KTY: "RSA"}}})
	}))
	defer server.Close()
	jwks, err := FetchAgentIdentityJWKS(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("FetchAgentIdentityJWKS() error = %v", err)
	}
	if jwks == nil || len(jwks.Keys) != 1 || jwks.Keys[0].KID != "kid" {
		t.Fatalf("jwks = %#v", jwks)
	}
}

func TestURLs(t *testing.T) {
	if AgentRegistrationURL("https://auth.openai.com/api/accounts/") != "https://auth.openai.com/api/accounts/v1/agent/register" {
		t.Fatalf("registration url mismatch")
	}
	if AgentRegistrationURL("http://localhost:8080/backend-api") != "http://localhost:8080/backend-api/v1/agent/register" {
		t.Fatalf("registration backend-api url mismatch")
	}
	if AgentTaskRegistrationURL("https://auth.openai.com/api/accounts", "agent") != "https://auth.openai.com/api/accounts/v1/agent/agent/task/register" {
		t.Fatalf("task url mismatch")
	}
	if JWKSURL("https://chatgpt.com/backend-api") != "https://chatgpt.com/backend-api/wham/agent-identities/jwks" {
		t.Fatalf("jwks url mismatch")
	}
	if JWKSURL("http://localhost:8080/api/codex/") != "http://localhost:8080/api/codex/agent-identities/jwks" {
		t.Fatalf("custom jwks url mismatch")
	}
}

func TestEnvironmentBaseURLs(t *testing.T) {
	prod := ChatGPTProduction
	if prod.ChatGPTBaseURL() != "https://chatgpt.com/backend-api" {
		t.Fatalf("prod ChatGPTBaseURL = %q", prod.ChatGPTBaseURL())
	}
	if prod.AuthAPIBaseURL() != "https://auth.openai.com/api/accounts" {
		t.Fatalf("prod AuthAPIBaseURL = %q", prod.AuthAPIBaseURL())
	}
	staging := ChatGPTStaging
	if staging.ChatGPTBaseURL() != "https://chatgpt-staging.com/backend-api" {
		t.Fatalf("staging ChatGPTBaseURL = %q", staging.ChatGPTBaseURL())
	}
	if staging.AuthAPIBaseURL() != "https://auth.api.openai.org/api/accounts" {
		t.Fatalf("staging AuthAPIBaseURL = %q", staging.AuthAPIBaseURL())
	}
}

func TestBuildABOM(t *testing.T) {
	cli := BuildABOM("1.2.3", "cli", "linux")
	if cli.AgentVersion != "1.2.3" || cli.AgentHarnessID != "codex-cli" || cli.RunningLocation != "cli-linux" {
		t.Fatalf("cli ABOM = %#v", cli)
	}
	vscode := BuildABOM("1.2.3", "vscode", "darwin")
	if vscode.AgentHarnessID != "codex-app" || vscode.RunningLocation != "vscode-darwin" {
		t.Fatalf("vscode ABOM = %#v", vscode)
	}
}

func TestRetryableRegistrationError(t *testing.T) {
	if !IsRetryableRegistrationError(&RegistrationHTTPError{StatusCode: http.StatusTooManyRequests}) {
		t.Fatalf("429 should be retryable")
	}
	if !IsRetryableRegistrationError(&RegistrationHTTPError{StatusCode: http.StatusServiceUnavailable}) {
		t.Fatalf("503 should be retryable")
	}
	if IsRetryableRegistrationError(&RegistrationHTTPError{StatusCode: http.StatusForbidden}) {
		t.Fatalf("403 should not be retryable")
	}
}

func testIdentityKey(t *testing.T) (*IdentityKey, ed25519.PublicKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	return &IdentityKey{AgentRuntimeID: "agent", PrivateKeyPKCS8Base64: base64.StdEncoding.EncodeToString(der)}, publicKey
}

func jwkFromRSAPublicKey(kid string, publicKey *rsa.PublicKey) JWK {
	return JWK{
		KID: kid,
		KTY: "RSA",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(publicKey.E)).Bytes()),
	}
}

func writeJSONAgent(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}
