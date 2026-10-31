package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
)

// Mirrors Rust #56055: a run whose effective requirements select a
// customer-hosted configuration server must reject a bundle applied from any
// other server, while the matching server loads normally.
func TestEffectiveConfigRejectsAppliedConfigServerMismatchLikeRust(t *testing.T) {
	home := t.TempDir()
	requirements := "[config_server]\nbase_url = \"https://required.example.com/settings\"\n"
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
		t.Fatal(err)
	}
	options := func(loader *config.CloudConfigLoader) *config.EffectiveOptions {
		return &config.EffectiveOptions{
			IncludeManagedConfig: true,
			ManagedConfigPath:    filepath.Join(home, "managed_config.toml"),
			CloudConfigBundle:    loader,
		}
	}
	newLoader := func(server *config.ConfigServer) *config.CloudConfigLoader {
		loader := config.NewCloudConfigLoader(func() (*config.CloudConfigBundle, error) {
			return &config.CloudConfigBundle{}, nil
		})
		if server != nil {
			loader = loader.WithConfigServer(server)
		}
		return loader
	}

	other := newLoader(&config.ConfigServer{BaseURL: "https://other.example.com/settings"})
	if _, err := loadEffectiveWithAppliedConfigServer(home, options(other), other); !errors.Is(err, config.ErrConfigServerMismatch) {
		t.Fatalf("mismatched applied server error = %v, want ErrConfigServerMismatch", err)
	}

	if _, err := loadEffectiveWithAppliedConfigServer(home, options(nil), nil); !errors.Is(err, config.ErrConfigServerMismatch) {
		t.Fatalf("missing applied server error = %v, want ErrConfigServerMismatch", err)
	}

	required := newLoader(&config.ConfigServer{BaseURL: "https://required.example.com/settings"})
	if _, err := loadEffectiveWithAppliedConfigServer(home, options(required), required); err != nil {
		t.Fatalf("matching applied server error = %v", err)
	}
}
