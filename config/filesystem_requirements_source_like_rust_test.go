package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestFilesystemRequirementsSourceTracksLayersLikeRust covers Rust #42821:
// doctor names the managed [permissions.filesystem] source as system for the
// local requirements.toml, cloud for enterprise-managed fragments, and
// multiple layers when both contribute.
func TestFilesystemRequirementsSourceTracksLayersLikeRust(t *testing.T) {
	writeRequirements := func(t *testing.T, home string) {
		t.Helper()
		denied := fmt.Sprintf("%q", filepath.Join(home, "private"))
		contents := "[permissions.filesystem]\ndeny_read = [" + denied + "]\n"
		if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(contents), 0o600); err != nil {
			t.Fatalf("write requirements.toml: %v", err)
		}
	}
	cloudLoader := func(t *testing.T, home string) *CloudConfigLoader {
		t.Helper()
		denied := fmt.Sprintf("%q", filepath.Join(home, "cloud-private"))
		bundle := CloudConfigBundle{RequirementsTOML: CloudConfigRequirementsTOMLBundle{
			EnterpriseManaged: []CloudConfigFragment{{
				ID: "doctor-policy", Name: "Doctor policy",
				Contents: "[permissions.filesystem]\ndeny_read = [" + denied + "]\n",
			}},
		}}
		return NewCloudConfigLoader(func() (*CloudConfigBundle, error) { return &bundle, nil })
	}
	load := func(t *testing.T, home string, loader *CloudConfigLoader) *Config {
		t.Helper()
		opts := &LoadOptions{IgnoreProjectConfig: true}
		if loader != nil {
			opts.IncludeManagedConfig = true
			opts.ManagedConfigPath = filepath.Join(home, "managed_config.toml")
			opts.CloudConfigBundle = loader
		}
		cfg, err := LoadWithOptions(home, opts)
		if err != nil {
			t.Fatalf("LoadWithOptions: %v", err)
		}
		return cfg
	}

	if got := load(t, t.TempDir(), nil).FilesystemRequirementsSource(); got != "none" {
		t.Fatalf("no requirements source = %q, want none", got)
	}

	localHome := t.TempDir()
	writeRequirements(t, localHome)
	if got := load(t, localHome, nil).FilesystemRequirementsSource(); got != "system" {
		t.Fatalf("local requirements source = %q, want system", got)
	}

	cloudHome := t.TempDir()
	if got := load(t, cloudHome, cloudLoader(t, cloudHome)).FilesystemRequirementsSource(); got != "cloud" {
		t.Fatalf("cloud requirements source = %q, want cloud", got)
	}

	bothHome := t.TempDir()
	writeRequirements(t, bothHome)
	if got := load(t, bothHome, cloudLoader(t, bothHome)).FilesystemRequirementsSource(); got != "multiple layers" {
		t.Fatalf("composite requirements source = %q, want multiple layers", got)
	}

	manual := &Config{Requirements: &ConfigRequirements{Permissions: map[string]any{"filesystem": map[string]any{}}}}
	if got := manual.FilesystemRequirementsSource(); got != "unknown" {
		t.Fatalf("manual requirements source = %q, want unknown", got)
	}
}
