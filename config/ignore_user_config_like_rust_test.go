package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Mirrors Rust's config_loader_tests::ignore_user_config_keeps_empty_user_layer
// (codex-rs/core/src/config/config_loader_tests.rs): with ignore_user_config the
// user file is never parsed -- an invalid file does not fail the load -- and the
// User layer survives with no values.
func TestLoadWithOptionsIgnoreUserConfigKeepsEmptyUserLayerLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(ConfigPath(home), []byte("model = \"from-user-config\"\ninvalid = [\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(user config) error = %v", err)
	}
	if _, err := LoadWithOptions(home, nil); err == nil {
		t.Fatal("LoadWithOptions() accepted an unparseable user config without --ignore-user-config")
	}
	cfg, err := LoadWithOptions(home, &LoadOptions{IgnoreUserConfig: true})
	if err != nil {
		t.Fatalf("LoadWithOptions(IgnoreUserConfig) error = %v", err)
	}
	if got := cfg.Values["model"]; got == "from-user-config" {
		t.Fatalf("model = %v, want the ignored user value to be absent", got)
	}
	var userLayer *Layer
	for index := range cfg.featureLayers {
		layer := &cfg.featureLayers[index]
		if layer.Name.Type != LayerSourceUser || layer.Name.Profile != nil {
			continue
		}
		userLayer = layer
	}
	if userLayer == nil {
		t.Fatal("user layer missing: the ignored config must still occupy its precedence slot")
	}
	if userLayer.Name.File != ConfigPath(home) {
		t.Fatalf("user layer file = %q, want %q", userLayer.Name.File, ConfigPath(home))
	}
	values, ok := userLayer.Config.(map[string]any)
	if !ok {
		t.Fatalf("user layer config = %T, want a table", userLayer.Config)
	}
	if len(values) != 0 {
		t.Fatalf("user layer config = %#v, want an empty table", values)
	}
}

// Acceptance: packaged defaults, the managed layer and the project layer keep
// working; only the user config file is skipped. The trust grant lives in the
// managed layer here on purpose, so the project layer still loads and this test
// cannot be satisfied by accidentally disabling project discovery.
func TestLoadWithOptionsIgnoreUserConfigKeepsTheOtherLayers(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("MkdirAll(home) error = %v", err)
	}
	repo := initTrustRepo(t, root)
	if err := os.MkdirAll(filepath.Join(repo, ".gcode"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.gcode) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gcode", "config.toml"), []byte("model_verbosity = \"medium\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(project config) error = %v", err)
	}
	packagedPath := filepath.Join(root, "packaged.toml")
	if err := os.WriteFile(packagedPath, []byte("model = \"from-packaged\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(packaged defaults) error = %v", err)
	}
	managedPath := filepath.Join(root, "managed_config.toml")
	managed := "approval_policy = \"never\"\n[projects." + tomlBasicString(canonicalProjectPath(repo)) + "]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(managedPath, []byte(managed), 0o644); err != nil {
		t.Fatalf("WriteFile(managed config) error = %v", err)
	}
	user := "model = \"from-user\"\nreview_model = \"from-user-review\"\n"
	if err := os.WriteFile(ConfigPath(home), []byte(user), 0o644); err != nil {
		t.Fatalf("WriteFile(user config) error = %v", err)
	}
	options := func(ignoreUserConfig bool) *LoadOptions {
		return &LoadOptions{
			PackagedDefaultsPath: packagedPath,
			IncludeManagedConfig: true,
			ManagedConfigPath:    managedPath,
			CWD:                  repo,
			IgnoreUserConfig:     ignoreUserConfig,
		}
	}
	baseline, err := LoadWithOptions(home, options(false))
	if err != nil {
		t.Fatalf("LoadWithOptions(baseline) error = %v", err)
	}
	if got := baseline.Values["model"]; got != "from-user" {
		t.Fatalf("baseline model = %v, want the user value", got)
	}
	ignored, err := LoadWithOptions(home, options(true))
	if err != nil {
		t.Fatalf("LoadWithOptions(ignore) error = %v", err)
	}
	if got := ignored.Values["model"]; got != "from-packaged" {
		t.Fatalf("ignored model = %v, want the packaged defaults value", got)
	}
	if got := ignored.Values["review_model"]; got != nil {
		t.Fatalf("ignored review_model = %v, want the user value to be dropped", got)
	}
	if got := ignored.Values["approval_policy"]; got != "never" {
		t.Fatalf("ignored approval_policy = %v, want the managed value", got)
	}
	if got := ignored.Values["model_verbosity"]; got != "medium" {
		t.Fatalf("ignored model_verbosity = %v, want the project value", got)
	}
}

// Acceptance: a trust_level saved in the user config must not be honoured under
// --ignore-user-config. The project layer is then untrusted, so its
// configuration stops loading and the exec path keeps printing the warning --
// the same reason `--trust` and `--ignore-user-config` are mutually exclusive.
func TestIgnoreUserConfigLeavesSavedProjectTrustUnused(t *testing.T) {
	fixture := newExecTrustFixture(t, "")
	trusted := "[projects." + tomlBasicString(canonicalProjectPath(fixture.repo)) + "]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(ConfigPath(fixture.codexHome), []byte(trusted), 0o644); err != nil {
		t.Fatalf("WriteFile(user config) error = %v", err)
	}
	projectConfigPath := filepath.Join(fixture.repo, ".gcode", "config.toml")

	baseline, err := LoadWithOptions(fixture.codexHome, &LoadOptions{CWD: fixture.repo})
	if err != nil {
		t.Fatalf("LoadWithOptions(baseline) error = %v", err)
	}
	if got := baseline.Values["model"]; got != "project-model" {
		t.Fatalf("baseline model = %v, want the trusted project value", got)
	}
	ignored, err := LoadWithOptions(fixture.codexHome, &LoadOptions{CWD: fixture.repo, IgnoreUserConfig: true})
	if err != nil {
		t.Fatalf("LoadWithOptions(ignore) error = %v", err)
	}
	if got := ignored.Values["model"]; got == "project-model" {
		t.Fatalf("model = %v, want the untrusted project config to be skipped", got)
	}

	service := NewConfigService(fixture.codexHome)
	if got := service.ExecProjectTrustWarningForOptions(fixture.repo, false); got != "" {
		t.Fatalf("warning = %q, want none while the user trust decision applies", got)
	}
	if got := service.ExecProjectTrustWarningForOptions(fixture.repo, true); got != ExecProjectTrustWarning {
		t.Fatalf("warning under --ignore-user-config = %q, want the exec trust warning", got)
	}
	if _, err := os.Stat(projectConfigPath); err != nil {
		t.Fatalf("project config missing from the fixture: %v", err)
	}
}
