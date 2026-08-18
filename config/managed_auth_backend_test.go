package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestManagedAuthBackendRequirementsOverrideUserConfigLikeRust mirrors Rust
// config_requirements tests (#39043): exact managed cli_auth_credentials_store
// and chatgpt_base_url requirements override user-configured values in the
// effective config (runtime and bootstrap authentication).
func TestManagedAuthBackendRequirementsOverrideUserConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "cli_auth_credentials_store = \"file\"\nchatgpt_base_url = \"https://user.example/backend-api/\"\n")
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("cli_auth_credentials_store = \"keyring\"\nchatgpt_base_url = \"https://managed.example/backend-api/\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadEffectiveWithOptions(home, &EffectiveOptions{})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
	}
	if got := cfg.CLIAuthCredentialsStoreMode(); got != "keyring" {
		t.Fatalf("CLIAuthCredentialsStoreMode = %q, want managed keyring override", got)
	}
	if got := cfg.ChatGPTBaseURL(); got != "https://managed.example/backend-api/" {
		t.Fatalf("ChatGPTBaseURL = %q, want managed override", got)
	}
}

// TestManagedAuthBackendRequirementsExposedViaReadLikeRust verifies the managed
// values surface through configRequirements/read (ConfigService.Requirements).
func TestManagedAuthBackendRequirementsExposedViaReadLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("cli_auth_credentials_store = \"auto\"\nchatgpt_base_url = \"https://managed.example/backend-api/\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	requirements, err := LoadRequirementsFile(filepath.Join(home, "requirements.toml"))
	if err != nil {
		t.Fatalf("LoadRequirementsFile() error = %v", err)
	}
	if requirements == nil || requirements.CliAuthCredentialsStore == nil || *requirements.CliAuthCredentialsStore != AuthCredentialsStoreAuto {
		t.Fatalf("requirements.CliAuthCredentialsStore = %#v", requirements)
	}
	if requirements.ChatgptBaseURL == nil || *requirements.ChatgptBaseURL != "https://managed.example/backend-api/" {
		t.Fatalf("requirements.ChatgptBaseURL = %v", requirements.ChatgptBaseURL)
	}
	service := NewConfigService(home)
	service.SetRequirements(requirements)
	read := service.Requirements()
	if read == nil || read.Requirements == nil || read.Requirements.CliAuthCredentialsStore == nil || *read.Requirements.CliAuthCredentialsStore != AuthCredentialsStoreAuto {
		t.Fatalf("Requirements() = %#v", read)
	}
	if read.Requirements.ChatgptBaseURL == nil || *read.Requirements.ChatgptBaseURL != "https://managed.example/backend-api/" {
		t.Fatalf("Requirements().ChatgptBaseURL = %v", read.Requirements.ChatgptBaseURL)
	}
}

// TestManagedAuthBackendWriteRejectedLikeRust verifies config write APIs reject
// changing exact managed authentication requirements while unrelated keys stay
// writable, and the keys remain writable without managed requirements.
func TestManagedAuthBackendWriteRejectedLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "model = \"gpt-5\"\n")
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("cli_auth_credentials_store = \"keyring\"\nchatgpt_base_url = \"https://managed.example/backend-api/\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	requirements, err := LoadRequirementsFile(filepath.Join(home, "requirements.toml"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(home)
	service.SetRequirements(requirements)

	for _, keyPath := range []string{"cli_auth_credentials_store", "chatgpt_base_url"} {
		if _, err := service.WriteValue(&ConfigValueWriteParams{KeyPath: keyPath, Value: "changed", MergeStrategy: MergeReplace}); err == nil {
			t.Fatalf("WriteValue(%s) succeeded, want managed write rejection", keyPath)
		}
	}
	if _, err := service.WriteValue(&ConfigValueWriteParams{KeyPath: "model", Value: "gpt-5.1", MergeStrategy: MergeReplace}); err != nil {
		t.Fatalf("WriteValue(model) error = %v, want unrelated keys writable", err)
	}

	unmanaged := NewConfigService(t.TempDir())
	if _, err := unmanaged.WriteValue(&ConfigValueWriteParams{KeyPath: "cli_auth_credentials_store", Value: "file", MergeStrategy: MergeReplace}); err != nil {
		t.Fatalf("WriteValue(cli_auth_credentials_store) without requirements error = %v", err)
	}
}

// TestManagedAuthBackendInvalidModeRejectedLikeRust verifies requirements
// loading rejects unsupported credentials-store modes.
func TestManagedAuthBackendInvalidModeRejectedLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("cli_auth_credentials_store = \"bogus\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRequirementsFile(filepath.Join(home, "requirements.toml")); err == nil || !strings.Contains(err.Error(), "cli_auth_credentials_store") {
		t.Fatalf("LoadRequirementsFile() error = %v, want invalid mode rejection", err)
	}
}

// TestManagedAuthBackendIgnoredInCloudRequirementsLikeRust mirrors Rust
// "ignore local-only authentication requirements in cloud-managed requirement
// layers": cloud fragments cannot override them and the local requirement wins.
func TestManagedAuthBackendIgnoredInCloudRequirementsLikeRust(t *testing.T) {
	baseDir := t.TempDir()
	local := &ConfigRequirements{
		CliAuthCredentialsStore: authCredentialsStoreModePtr(AuthCredentialsStoreKeyring),
		ChatgptBaseURL:          stringPtr("https://local.example/backend-api/"),
	}
	bundle := CloudConfigBundle{
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{
			{ID: "cloud", Name: "Cloud", Contents: "cli_auth_credentials_store = \"ephemeral\"\nchatgpt_base_url = \"https://cloud.example/backend-api/\"\n"},
		}},
	}
	merged, err := applyCloudConfigBundle(map[string]any{}, local, bundle, baseDir)
	if err != nil {
		t.Fatalf("applyCloudConfigBundle() error = %v", err)
	}
	if merged == nil || merged.CliAuthCredentialsStore == nil || *merged.CliAuthCredentialsStore != AuthCredentialsStoreKeyring {
		t.Fatalf("merged.CliAuthCredentialsStore = %#v, want local keyring (cloud ignored)", merged)
	}
	if merged.ChatgptBaseURL == nil || *merged.ChatgptBaseURL != "https://local.example/backend-api/" {
		t.Fatalf("merged.ChatgptBaseURL = %v, want local (cloud ignored)", merged.ChatgptBaseURL)
	}
}

func authCredentialsStoreModePtr(mode AuthCredentialsStoreMode) *AuthCredentialsStoreMode {
	return &mode
}
