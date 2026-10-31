package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Rust #57175: the app server loads the latest managed configuration on every
// read, so a rewritten app-server managed config is observed without a
// restart.
func TestConfigServiceReadPicksUpRewrittenManagedConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	managed := filepath.Join(home, "app-server-managed.toml")
	if err := os.WriteFile(managed, []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatalf("write managed config: %v", err)
	}
	t.Setenv(appServerManagedConfigPathEnv, managed)
	service := NewConfigService(home)

	first, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if first.Config["model"] != "gpt-5" {
		t.Fatalf("first read model = %#v, want gpt-5", first.Config["model"])
	}

	if err := os.WriteFile(managed, []byte("model = \"gpt-6\"\n"), 0o600); err != nil {
		t.Fatalf("rewrite managed config: %v", err)
	}
	second, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("second Read() error = %v", err)
	}
	if second.Config["model"] != "gpt-6" {
		t.Fatalf("second read model = %#v, want the refreshed gpt-6", second.Config["model"])
	}
}

// Rust #57175 reloads requirements alongside the managed configuration.
func TestConfigServiceReadReloadsManagedRequirementsLikeRust(t *testing.T) {
	home := t.TempDir()
	requirementsPath := filepath.Join(home, "requirements.toml")
	if err := os.WriteFile(requirementsPath, []byte("chatgpt_base_url = \"https://first.example.test/backend-api/\"\n"), 0o600); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	service := NewConfigService(home)
	if got := service.Requirements().Requirements; got == nil || got.ChatgptBaseURL == nil || *got.ChatgptBaseURL != "https://first.example.test/backend-api/" {
		t.Fatalf("initial requirements = %#v, want the first base URL", got)
	}

	if err := os.WriteFile(requirementsPath, []byte("chatgpt_base_url = \"https://second.example.test/backend-api/\"\n"), 0o600); err != nil {
		t.Fatalf("rewrite requirements: %v", err)
	}
	if _, err := service.Read(&ConfigReadParams{}); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := service.Requirements().Requirements; got == nil || got.ChatgptBaseURL == nil || *got.ChatgptBaseURL != "https://second.example.test/backend-api/" {
		t.Fatalf("refreshed requirements = %#v, want the second base URL", got)
	}
}

// A host-installed managed layer stack still wins over file refreshes.
func TestConfigServiceSetManagedLayersWinsOverFileRefreshLikeRust(t *testing.T) {
	home := t.TempDir()
	managed := filepath.Join(home, "app-server-managed.toml")
	if err := os.WriteFile(managed, []byte("model = \"file-model\"\n"), 0o600); err != nil {
		t.Fatalf("write managed config: %v", err)
	}
	t.Setenv(appServerManagedConfigPathEnv, managed)
	service := NewConfigService(home)
	service.SetManagedLayers([]Layer{{
		Name:   LayerSource{Type: LayerSourceLegacyManagedConfigFromFile, File: "host"},
		Config: map[string]any{"model": "host-model"},
	}})
	if _, err := service.Read(&ConfigReadParams{}); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if err := os.WriteFile(managed, []byte("model = \"file-model-2\"\n"), 0o600); err != nil {
		t.Fatalf("rewrite managed config: %v", err)
	}
	read, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("second Read() error = %v", err)
	}
	if read.Config["model"] != "host-model" {
		t.Fatalf("model = %#v, want the host-installed layer", read.Config["model"])
	}
}

// Rust #57175 keeps the config server's managed-configuration loader and its
// refresh worker, so each read observes the latest fetched bundle.
func TestConfigServiceReadAppliesLatestCloudConfigBundleLikeRust(t *testing.T) {
	home := t.TempDir()
	version := "first"
	loader := NewCloudConfigLoader(func() (*CloudConfigBundle, error) {
		return &CloudConfigBundle{ConfigTOML: CloudConfigTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{
			ID:       "policy",
			Name:     "policy",
			Contents: "model = \"" + version + "\"",
		}}}}, nil
	})
	service := NewConfigService(home)
	service.SetCloudConfigLoader(loader)

	first, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if first.Config["model"] != "first" {
		t.Fatalf("first bundle model = %#v, want first", first.Config["model"])
	}

	version = "second"
	second, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("second Read() error = %v", err)
	}
	if second.Config["model"] != "second" {
		t.Fatalf("refreshed bundle model = %#v, want second", second.Config["model"])
	}
}

// A missing loader leaves the read path unchanged.
func TestConfigServiceReadWithoutCloudConfigLoaderKeepsValuesLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(ConfigPath(home), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	service := NewConfigService(home)
	read, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Config["model"] != "gpt-5" {
		t.Fatalf("model = %#v, want gpt-5", read.Config["model"])
	}
}

// Rust #56055 runtime_refresh: a refreshed bundle must come from the
// configuration server the local requirements select.
func TestConfigServiceReadRejectsSwappedConfigServerLikeRust(t *testing.T) {
	home := t.TempDir()
	requirementsPath := filepath.Join(home, "requirements.toml")
	requirements := "[config_server]\nbase_url = \"https://required.example.com/settings\"\n"
	if err := os.WriteFile(requirementsPath, []byte(requirements), 0o600); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	loader := NewCloudConfigLoader(func() (*CloudConfigBundle, error) {
		return &CloudConfigBundle{ConfigTOML: CloudConfigTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{
			ID:       "policy",
			Name:     "policy",
			Contents: "model = \"gpt-5\"",
		}}}}, nil
	}).WithConfigServer(&ConfigServer{BaseURL: "https://other.example.com/settings"})
	service := NewConfigService(home)
	service.SetCloudConfigLoader(loader)

	if _, err := service.Read(&ConfigReadParams{}); !errors.Is(err, ErrConfigServerMismatch) {
		t.Fatalf("Read() error = %v, want an ErrConfigServerMismatch", err)
	}

	loader.WithConfigServer(&ConfigServer{BaseURL: "https://required.example.com/settings"})
	read, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("Read() after matching server error = %v", err)
	}
	if read.Config["model"] != "gpt-5" {
		t.Fatalf("model = %#v, want gpt-5 from the matching server", read.Config["model"])
	}
}
