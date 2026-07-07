package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSaveLoadDelete(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(FromAPIKey(" sk-test ")); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil auth")
	}
	if loaded.OpenAIAPIKey != "sk-test" {
		t.Fatalf("OpenAIAPIKey = %q", loaded.OpenAIAPIKey)
	}
	if loaded.Mode() != "api-key" {
		t.Fatalf("Mode = %q", loaded.Mode())
	}
	removed, err := store.Delete()
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if !removed {
		t.Fatal("Delete removed = false, want true")
	}
	loaded, err = store.Load()
	if err != nil {
		t.Fatalf("Load after delete returned error: %v", err)
	}
	if loaded != nil {
		t.Fatalf("Load after delete = %#v, want nil", loaded)
	}
}

func TestDefaultCodexHomeUsesEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	if got := DefaultCodexHome(); got != dir {
		t.Fatalf("DefaultCodexHome = %q, want %q", got, dir)
	}
}

func TestSaveCreatesAuthJSON(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Save(FromAccessToken("pat-test")); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("auth.json should end with newline: %q", string(data))
	}
}

func TestKeyringStoreModePersistsOutsideAuthJSON(t *testing.T) {
	dir := t.TempDir()
	keyring := NewKeyringStore(KeyringBackendDirect)
	store := NewStoreWithOptions(dir, &StoreOptions{
		Mode:         AuthCredentialsStoreKeyring,
		KeyringStore: keyring,
	})
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("stale"), 0o600); err != nil {
		t.Fatalf("WriteFile stale auth returned error: %v", err)
	}
	if err := store.Save(FromAPIKey("sk-keyring")); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth.json should be removed after keyring save: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if loaded == nil || loaded.OpenAIAPIKey != "sk-keyring" {
		t.Fatalf("loaded auth = %+v", loaded)
	}
	resolved, err := store.Resolve()
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved == nil || resolved.Source != "keyring" {
		t.Fatalf("resolved = %+v", resolved)
	}
	removed, err := store.Delete()
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if !removed {
		t.Fatal("Delete removed = false, want true")
	}
}

func TestAutoStoreModeFallsBackToFileWhenKeyringEmpty(t *testing.T) {
	dir := t.TempDir()
	fileStore := NewStore(dir)
	if err := fileStore.Save(FromAPIKey("sk-file")); err != nil {
		t.Fatalf("file Save returned error: %v", err)
	}
	autoStore := NewStoreWithOptions(dir, &StoreOptions{Mode: AuthCredentialsStoreAuto})
	loaded, err := autoStore.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if loaded == nil || loaded.OpenAIAPIKey != "sk-file" {
		t.Fatalf("loaded auth = %+v", loaded)
	}
}

func TestEphemeralStoreModeIsInMemoryOnly(t *testing.T) {
	dir := t.TempDir()
	store := NewStoreWithOptions(dir, &StoreOptions{Mode: AuthCredentialsStoreEphemeral})
	if err := store.Save(FromAPIKey("sk-ephemeral")); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth.json should not exist for ephemeral store: %v", err)
	}
	loaded, err := NewStoreWithOptions(dir, &StoreOptions{Mode: AuthCredentialsStoreEphemeral}).Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if loaded == nil || loaded.OpenAIAPIKey != "sk-ephemeral" {
		t.Fatalf("loaded auth = %+v", loaded)
	}
	removed, err := store.Delete()
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if !removed {
		t.Fatal("Delete removed = false, want true")
	}
}

func TestResolvePrefersEnv(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(FromAPIKey("sk-file")); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	t.Setenv(OpenAIAPIKeyEnv, " sk-env ")
	resolved, err := store.Resolve()
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved == nil {
		t.Fatal("Resolve returned nil")
	}
	if resolved.Source != OpenAIAPIKeyEnv {
		t.Fatalf("Source = %q", resolved.Source)
	}
	if resolved.Auth.OpenAIAPIKey != "sk-env" {
		t.Fatalf("OpenAIAPIKey = %q", resolved.Auth.OpenAIAPIKey)
	}
}

func TestResolveClassifiesCodexAccessTokenEnv(t *testing.T) {
	store := NewStore(t.TempDir())
	t.Setenv(CodexAccessTokenEnv, "at-env-token")
	resolved, err := store.Resolve()
	if err != nil {
		t.Fatalf("Resolve PAT returned error: %v", err)
	}
	if resolved == nil || resolved.Auth.Mode() != "personal-access-token" || resolved.Auth.PersonalAccessToken != "at-env-token" {
		t.Fatalf("resolved PAT = %+v", resolved)
	}

	t.Setenv(CodexAccessTokenEnv, "header.payload.sig")
	resolved, err = store.Resolve()
	if err != nil {
		t.Fatalf("Resolve JWT returned error: %v", err)
	}
	if resolved == nil || resolved.Auth.Mode() != "agent-identity" || resolved.Auth.AgentIdentity != "header.payload.sig" {
		t.Fatalf("resolved JWT = %+v", resolved)
	}
}

func TestFromBedrockAPIKeyUsesRustAuthShape(t *testing.T) {
	snapshot := FromBedrockAPIKey(" bedrock-key ", " us-east-2 ")
	if snapshot.Mode() != "bedrock-api-key" {
		t.Fatalf("Mode = %q", snapshot.Mode())
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	var decoded struct {
		AuthMode      string            `json:"auth_mode"`
		BedrockAPIKey BedrockAPIKeyAuth `json:"bedrock_api_key"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if decoded.AuthMode != "bedrock-api-key" || decoded.BedrockAPIKey.APIKey != "bedrock-key" || decoded.BedrockAPIKey.Region != "us-east-2" {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestSafeFormatSecret(t *testing.T) {
	if got := SafeFormatSecret("sk-1234567890"); got != "sk-1...7890" {
		t.Fatalf("SafeFormatSecret = %q", got)
	}
	if got := SafeFormatSecret("short"); got != "****" {
		t.Fatalf("SafeFormatSecret(short) = %q", got)
	}
}
