package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
)

func configServerCheckHome(t *testing.T, baseURL string) string {
	t.Helper()
	home := t.TempDir()
	body := "[config_server]\nbase_url = \"" + baseURL + "\"\n"
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return home
}

func configServerLoader(server *config.ConfigServer, model string) *config.CloudConfigLoader {
	loader := config.NewCloudConfigLoader(func() (*config.CloudConfigBundle, error) {
		if model == "" {
			return &config.CloudConfigBundle{}, nil
		}
		return &config.CloudConfigBundle{ConfigTOML: config.CloudConfigTOMLBundle{EnterpriseManaged: []config.CloudConfigFragment{{
			ID: "managed", Name: "managed", Contents: "model = \"" + model + "\"",
		}}}}, nil
	})
	return loader.WithConfigServer(server)
}

// Mirrors Rust #57175: doctor reports the managed bundle of the required
// configuration server instead of ignoring it.
func TestConfigCheckAppliesRequiredConfigServerLikeRust(t *testing.T) {
	required := &config.ConfigServer{BaseURL: "https://required.example.com/settings"}
	home := configServerCheckHome(t, required.BaseURL)
	check := configCheck(home, &Options{ManagedConfigLoader: func(context.Context, string, *config.Config) (*config.CloudConfigLoader, error) {
		return configServerLoader(required, "gpt-5-managed"), nil
	}})
	if check.ID != "config.load" || check.Status == CheckStatusFail {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "model: gpt-5-managed") {
		t.Fatalf("managed model missing from %#v", check.Details)
	}
}

// Mirrors Rust #57175: a bundle applied from a server other than the required
// one fails the configuration check.
func TestConfigCheckRejectsMismatchedConfigServerLikeRust(t *testing.T) {
	required := &config.ConfigServer{BaseURL: "https://required.example.com/settings"}
	home := configServerCheckHome(t, required.BaseURL)
	check := configCheck(home, &Options{ManagedConfigLoader: func(context.Context, string, *config.Config) (*config.CloudConfigLoader, error) {
		return configServerLoader(&config.ConfigServer{BaseURL: "https://other.example.com/settings"}, "gpt-5-managed"), nil
	}})
	if check.ID != "config.load" || check.Status != CheckStatusFail {
		t.Fatalf("check = %+v, want a failed config load", check)
	}
}

// A configuration that requires a config server but has no loader to apply one
// cannot be reported as loaded (Rust #57175).
func TestConfigCheckRequiresAppliedConfigServerLikeRust(t *testing.T) {
	required := &config.ConfigServer{BaseURL: "https://required.example.com/settings"}
	home := configServerCheckHome(t, required.BaseURL)
	check := configCheck(home, &Options{})
	if check.ID != "config.load" || check.Status != CheckStatusFail {
		t.Fatalf("check = %+v, want a failed config load", check)
	}
	_, err := loadEffectiveConfigForDoctor(home, &Options{})
	if !errors.Is(err, config.ErrConfigServerMismatch) {
		t.Fatalf("loadEffectiveConfigForDoctor() error = %v, want ErrConfigServerMismatch", err)
	}
	if !strings.Contains(err.Error(), "required configuration server bundle was not applied") {
		t.Fatalf("error = %v", err)
	}
}
