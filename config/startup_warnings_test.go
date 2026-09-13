package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStartupWarningsForRequirementsLikeRust pins the requirement-driven startup
// warnings: exact overrides for the fields Go applies, the explicit
// sandbox_private_desktop conflict, and the disallowed windows.sandbox fallback.
func TestStartupWarningsForRequirementsLikeRust(t *testing.T) {
	credentialMode := AuthCredentialsStoreKeyring
	baseURL := "https://managed.example.com"
	provider := "managed-provider"
	privateDesktop := false
	requirements := &ConfigRequirements{
		CliAuthCredentialsStore:      &credentialMode,
		ChatgptBaseURL:               &baseURL,
		ModelProvider:                &provider,
		WindowsSandboxPrivateDesktop: &privateDesktop,
		AllowedWindowsSandboxImplementations: []WindowsSandboxSetupMode{
			WindowsSandboxSetupElevated,
		},
	}
	values := map[string]any{
		"cli_auth_credentials_store": "file",
		"chatgpt_base_url":           "https://user.example.com",
		"model_provider":             "user-provider",
		"windows": map[string]any{
			"sandbox":                 "unelevated",
			"sandbox_private_desktop": true,
		},
	}
	warnings := StartupWarnings(values, requirements)
	want := []string{
		"Configured value for `cli_auth_credentials_store` is overridden by the required value \"keyring\" from managed requirements.",
		"Configured value for `chatgpt_base_url` is overridden by the required value \"https://managed.example.com\" from managed requirements.",
		"Configured value for `model_provider` is overridden by the required value \"managed-provider\" from managed requirements.",
		"Configured value for `windows.sandbox_private_desktop` is overridden by the required value false from managed requirements.",
		"Configured value for `windows.sandbox` is disallowed by requirements; falling back to required value \"elevated\".",
	}
	if len(warnings) != len(want) {
		t.Fatalf("StartupWarnings() = %#v, want %#v", warnings, want)
	}
	for index, warning := range warnings {
		if warning != want[index] {
			t.Fatalf("warning[%d] = %q, want %q", index, warning, want[index])
		}
	}

	// Matching configured values produce no warnings, and the implicit private
	// desktop default is not a conflict.
	quiet := StartupWarnings(map[string]any{
		"cli_auth_credentials_store": "keyring",
		"chatgpt_base_url":           "https://managed.example.com",
		"model_provider":             "managed-provider",
		"windows":                    map[string]any{"sandbox": "elevated"},
	}, requirements)
	if len(quiet) != 0 {
		t.Fatalf("StartupWarnings() = %#v, want none", quiet)
	}
	if got := StartupWarnings(values, nil); got != nil {
		t.Fatalf("StartupWarnings() without requirements = %#v, want nil", got)
	}
}

// TestConfigServiceReadSurfacesStartupWarnings pins the app-server surfacing:
// config/read records the requirement-driven warnings like Rust's
// ConfigWarningNotification list.
func TestConfigServiceReadSurfacesStartupWarnings(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(ConfigPath(home), []byte("[windows]\nsandbox = \"unelevated\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("[windows]\nallowed_sandbox_implementations = [\"elevated\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(home)
	if _, err := service.Read(&ConfigReadParams{}); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	found := false
	for _, warning := range service.Warnings() {
		if strings.Contains(warning.Summary, "windows.sandbox") && strings.Contains(warning.Summary, "disallowed by requirements") {
			found = true
		}
	}
	if !found {
		t.Fatalf("config warnings = %#v, want the windows.sandbox fallback notice", service.Warnings())
	}
	// A repeated read must not duplicate the notice.
	if _, err := service.Read(&ConfigReadParams{}); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	count := 0
	for _, warning := range service.Warnings() {
		if strings.Contains(warning.Summary, "disallowed by requirements") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate startup warnings recorded: %d", count)
	}
}
