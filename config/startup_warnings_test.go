package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStartupWarningsForRequirementsLikeRust pins the requirement-driven startup
// warnings: exact overrides for the fields Go applies and the disallowed
// windows.sandbox fallback. Rust #46554 removed windows.sandbox_private_desktop,
// so it can no longer produce an override warning.
func TestStartupWarningsForRequirementsLikeRust(t *testing.T) {
	credentialMode := AuthCredentialsStoreKeyring
	baseURL := "https://managed.example.com"
	provider := "managed-provider"
	requirements := &ConfigRequirements{
		CliAuthCredentialsStore: &credentialMode,
		ChatgptBaseURL:          &baseURL,
		ModelProvider:           &provider,
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

// TestConfigWarningsForCWDAggregatesProducersLikeRust pins the shared app-server
// and exec entry point: unrecognized settings, malformed agent roles, and the
// requirement-driven warnings are reported together for the layers visible from
// cwd.
func TestConfigWarningsForCWDAggregatesProducersLikeRust(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	userConfig := "approval_policy = \"never\"\nunknown_setting = true\n\n[projects.\"" + strings.ReplaceAll(repo, `\`, `\\`) + "\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(ConfigPath(home), []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("allowed_approval_policies = [\"on-request\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dotCodex := filepath.Join(repo, ".gcode")
	if err := os.MkdirAll(dotCodex, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotCodex, "config.toml"), []byte("[agents.worker]\nnickname_candidates = [\"Scout\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	warnings := NewConfigService(home).ConfigWarningsForCWD(repo)
	var hasRequirement, hasIgnored, hasAgentRole bool
	for _, warning := range warnings {
		if strings.Contains(warning, "`approval_policy` is disallowed by requirements") {
			hasRequirement = true
		}
		if strings.Contains(warning, "unknown_setting") {
			hasIgnored = true
		}
		if strings.Contains(warning, "Ignoring malformed agent role definition: agent role `worker` must define a description") {
			hasAgentRole = true
		}
	}
	if !hasRequirement || !hasIgnored || !hasAgentRole {
		t.Fatalf("ConfigWarningsForCWD() = %#v", warnings)
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
