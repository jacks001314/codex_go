package appserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/session"
)

func createResumableProviderThread(t *testing.T, home string, provider string) string {
	t.Helper()
	store := session.NewStore(home)
	now := time.Now().UTC()
	record := &session.Record{
		ID: "thread-old-provider", SessionID: "thread-old-provider",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{ModelProvider: provider, CWD: home},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "saved history", CreatedAt: now},
		},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create record error = %v", err)
	}
	return string(record.ID)
}

// Rust #57840: resume reports a structured provider error instead of silently
// continuing, and model/reasoning overrides do not bypass the check.
func TestThreadResumeReportsProviderPolicyConflictLikeRust(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, managedProviderRequirements)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`
model_provider = 'old_provider'
[model_providers.old_provider]
name = 'Original provider'
base_url = 'https://old.example/v1'
`), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	threadID := createResumableProviderThread(t, home, "old_provider")
	router := newManagedProviderRouter(t, home)

	modelOverride := "gpt-5.1-codex"
	for _, params := range []*ThreadResumeParams{
		{ThreadID: threadID},
		{ThreadID: threadID, Model: &modelOverride},
		{ThreadID: threadID, Config: map[string]any{"model": "gpt-5.1-codex"}},
		{ThreadID: threadID, ModelProvider: strPtrValue("gateway")},
	} {
		response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, params))
		if response.Error == nil {
			t.Fatalf("resume %#v succeeded, want a provider policy conflict", params)
		}
		if response.Error.Code != -32600 {
			t.Fatalf("error code = %d, want -32600", response.Error.Code)
		}
		data := response.Error.Data
		if data == nil || data["type"] != "modelProviderPolicyConflict" || data["modelProvider"] != "old_provider" || data["requiredModelProvider"] != "gateway" {
			t.Fatalf("error data = %#v, want a policy conflict for old_provider/gateway", data)
		}
	}
}

// Rust #57840: a saved provider that no longer exists in the configuration
// reported a not-configured error.
func TestThreadResumeReportsNotConfiguredProviderLikeRust(t *testing.T) {
	home := t.TempDir()
	threadID := createResumableProviderThread(t, home, "old_provider")
	router := newManagedProviderRouter(t, home)

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, &ThreadResumeParams{ThreadID: threadID}))
	if response.Error == nil {
		t.Fatalf("resume succeeded, want a not-configured provider error")
	}
	data := response.Error.Data
	if data == nil || data["type"] != "modelProviderNotConfigured" || data["modelProvider"] != "old_provider" {
		t.Fatalf("error data = %#v, want modelProviderNotConfigured for old_provider", data)
	}
}

// Rust #57840: a configured provider that is not the selected one reports a
// mismatch, and restoring the selection makes resume succeed again.
func TestThreadResumeReportsProviderMismatchAndRecoversLikeRust(t *testing.T) {
	home := t.TempDir()
	providerDefinition := `
[model_providers.old_provider]
name = 'Original provider'
base_url = 'https://old.example/v1'
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model_provider = 'old_provider'\n"+providerDefinition), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	threadID := createResumableProviderThread(t, home, "old_provider")
	router := newManagedProviderRouter(t, home)

	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(providerDefinition), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, &ThreadResumeParams{ThreadID: threadID}))
	if response.Error == nil {
		t.Fatalf("resume succeeded, want a provider mismatch")
	}
	data := response.Error.Data
	if data == nil || data["type"] != "modelProviderMismatch" || data["modelProvider"] != "old_provider" || data["currentModelProvider"] != "openai" {
		t.Fatalf("error data = %#v, want a mismatch for old_provider/openai", data)
	}

	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model_provider = 'old_provider'\n"+providerDefinition), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	recovered := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, &ThreadResumeParams{ThreadID: threadID}))
	if recovered.Error != nil {
		t.Fatalf("resume after restoring the provider error = %+v", recovered.Error)
	}
}

func strPtrValue(value string) *string { return &value }
