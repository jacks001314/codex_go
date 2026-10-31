package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
)

// Rust #57175: the app server's ConfigManager keeps the managed-configuration
// loader and its refresh worker for the process lifetime, so protocol clients
// observe a rewritten managed config without restarting the server.
func TestRuntimeRouterConfigServiceKeepsManagedConfigFreshLikeRust(t *testing.T) {
	home := t.TempDir()
	managed := filepath.Join(home, "app-server-managed.toml")
	if err := os.WriteFile(managed, []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatalf("write managed config: %v", err)
	}
	t.Setenv("CODEX_APP_SERVER_MANAGED_CONFIG_PATH", managed)
	router := NewDefaultRuntimeRouterWithOptions(nil, home, nil)

	first, err := router.config.Read(&config.ConfigReadParams{})
	if err != nil {
		t.Fatalf("first Read() error = %v", err)
	}
	if first.Config["model"] != "gpt-5" {
		t.Fatalf("first read model = %#v, want gpt-5", first.Config["model"])
	}

	if err := os.WriteFile(managed, []byte("model = \"gpt-6\"\n"), 0o600); err != nil {
		t.Fatalf("rewrite managed config: %v", err)
	}
	second, err := router.config.Read(&config.ConfigReadParams{})
	if err != nil {
		t.Fatalf("second Read() error = %v", err)
	}
	if second.Config["model"] != "gpt-6" {
		t.Fatalf("second read model = %#v, want the refreshed gpt-6", second.Config["model"])
	}
}

// Rust #57175 wires the config server's managed-configuration loader into the
// app-server config manager, so each read observes the latest fetched bundle.
func TestRuntimeRouterConfigServiceAppliesLatestCloudConfigBundleLikeRust(t *testing.T) {
	version := "first"
	loader := config.NewCloudConfigLoader(func() (*config.CloudConfigBundle, error) {
		return &config.CloudConfigBundle{ConfigTOML: config.CloudConfigTOMLBundle{EnterpriseManaged: []config.CloudConfigFragment{{
			ID:       "policy",
			Name:     "policy",
			Contents: "model = \"" + version + "\"",
		}}}}, nil
	})
	router := NewDefaultRuntimeRouterWithOptions(nil, t.TempDir(), &RuntimeRouterOptions{CloudConfigLoader: loader})

	first, err := router.config.Read(&config.ConfigReadParams{})
	if err != nil {
		t.Fatalf("first Read() error = %v", err)
	}
	if first.Config["model"] != "first" {
		t.Fatalf("first bundle model = %#v, want first", first.Config["model"])
	}

	version = "second"
	second, err := router.config.Read(&config.ConfigReadParams{})
	if err != nil {
		t.Fatalf("second Read() error = %v", err)
	}
	if second.Config["model"] != "second" {
		t.Fatalf("refreshed bundle model = %#v, want second", second.Config["model"])
	}
}
