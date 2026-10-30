package auth

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust #58324: serde rejects any auth_mode value outside the AuthMode enum as a
// data error, and the credential-safe wrapper keeps the position but drops the
// rejected value.
func TestStoredAuthRejectsUnknownAuthModeLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"private-credential-value"}`), 0o600); err != nil {
		t.Fatalf("seed auth.json: %v", err)
	}
	_, err := NewStoreWithOptions(home, &StoreOptions{Mode: AuthCredentialsStoreFile}).Load()
	if err == nil {
		t.Fatal("unknown auth_mode loaded without error")
	}
	message := err.Error()
	if !strings.Contains(message, "failed to deserialize CLI auth from file") || !strings.Contains(message, "invalid credential JSON (Data at line 1 column ") {
		t.Fatalf("file storage error = %q", message)
	}
	if strings.Contains(message, "private-credential-value") || strings.Contains(message, home) {
		t.Fatalf("file storage error leaked credential data: %q", message)
	}
}

// The accepted spellings are the serde serialization of Rust's AuthMode enum
// plus the aliases Go has always written.
func TestStoredAuthAcceptsKnownAuthModesLikeRust(t *testing.T) {
	for _, mode := range []string{
		"apikey", "chatgpt", "chatgptAuthTokens", "headers", "agentIdentity",
		"personalAccessToken", "bedrockApiKey", "bedrockAccessKeys",
		"api-key", "agent-identity", "personal-access-token", "bedrock-api-key",
		"bedrock-access-keys",
	} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			payload := []byte(`{"auth_mode":"` + mode + `"}`)
			if err := os.WriteFile(filepath.Join(home, "auth.json"), payload, 0o600); err != nil {
				t.Fatalf("seed auth.json: %v", err)
			}
			loaded, err := NewStoreWithOptions(home, &StoreOptions{Mode: AuthCredentialsStoreFile}).Load()
			if err != nil {
				t.Fatalf("auth_mode %q rejected: %v", mode, err)
			}
			if loaded == nil || loaded.AuthMode != mode {
				t.Fatalf("loaded auth = %#v", loaded)
			}
		})
	}
}

// Rust #58324: agent identity JWT decode failures are reported with
// "failed to decode agent identity JWT" and never repeat claim values.
func TestValidateStoredAuthCredentialRedactsAgentIdentityJWTFailureLikeRust(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iat":"private-credential-value"}`))
	jwt := "header." + payload + ".signature"
	encoded, err := json.Marshal(map[string]any{"agent_identity": jwt})
	if err != nil {
		t.Fatalf("marshal auth payload: %v", err)
	}
	var snapshot AuthDotJSON
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatalf("unmarshal auth payload: %v", err)
	}
	err = ValidateStoredAuthCredential(&snapshot)
	if err == nil {
		t.Fatal("invalid agent identity JWT validated without error")
	}
	message := err.Error()
	if !strings.Contains(message, "failed to decode agent identity JWT") {
		t.Fatalf("agent identity error = %q", message)
	}
	if strings.Contains(message, "private-credential-value") {
		t.Fatalf("agent identity error leaked claim data: %q", message)
	}
	if err := ValidateStoredAuthCredential(&AuthDotJSON{AuthMode: "chatgpt"}); err != nil {
		t.Fatalf("ChatGPT auth rejected: %v", err)
	}
}
