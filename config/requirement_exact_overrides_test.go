package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestManagedExactRequirementsOverrideConfiguredValuesLikeRust pins Rust
// ConfigRequirementsToml::apply_to_config for the exact requirements Go applied
// silently before: sqlite_home, log_dir, model_catalog_json,
// check_for_update_on_startup, allow_login_shell, and feedback.enabled.
func TestManagedExactRequirementsOverrideConfiguredValuesLikeRust(t *testing.T) {
	home := t.TempDir()
	managedHome := filepath.Join(home, "managed-sqlite")
	managedLog := filepath.Join(home, "managed-log")
	managedCatalog := filepath.Join(home, "managed-models.json")
	configTOML := strings.Join([]string{
		`sqlite_home = "` + filepath.ToSlash(filepath.Join(home, "user-sqlite")) + `"`,
		`check_for_update_on_startup = true`,
		`allow_login_shell = true`,
		`[feedback]`,
		`enabled = true`,
	}, "\n") + "\n"
	if err := os.WriteFile(ConfigPath(home), []byte(configTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	requirementsTOML := strings.Join([]string{
		`sqlite_home = "` + filepath.ToSlash(managedHome) + `"`,
		`log_dir = "` + filepath.ToSlash(managedLog) + `"`,
		`model_catalog_json = "` + filepath.ToSlash(managedCatalog) + `"`,
		`check_for_update_on_startup = false`,
		`allow_login_shell = false`,
		`[feedback]`,
		`enabled = false`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirementsTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadEffective(home, nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadEffective() error = %v", err)
	}
	if got := cfg.SQLiteHome(); got == "" || !strings.Contains(filepath.ToSlash(got), "managed-sqlite") {
		t.Fatalf("SQLiteHome() = %q, want the required managed path", got)
	}
	if got, _ := cfg.Values["log_dir"].(string); !strings.Contains(filepath.ToSlash(got), "managed-log") {
		t.Fatalf("log_dir = %#v, want the required managed path", cfg.Values["log_dir"])
	}
	if got, _ := cfg.Values["model_catalog_json"].(string); !strings.Contains(filepath.ToSlash(got), "managed-models.json") {
		t.Fatalf("model_catalog_json = %#v, want the required managed path", cfg.Values["model_catalog_json"])
	}
	if cfg.Values["check_for_update_on_startup"] != false {
		t.Fatalf("check_for_update_on_startup = %#v, want the required false", cfg.Values["check_for_update_on_startup"])
	}
	if cfg.AllowLoginShell() {
		t.Fatal("AllowLoginShell() = true, want the required false")
	}
	feedback, ok := cfg.Values["feedback"].(map[string]any)
	if !ok || feedback["enabled"] != false {
		t.Fatalf("feedback = %#v, want the required enabled = false", cfg.Values["feedback"])
	}
}

// TestStartupWarningsForExactRequirementTailLikeRust pins the warning messages
// for the remaining exact requirements, including Rust's Debug rendering of the
// required values.
func TestStartupWarningsForExactRequirementTailLikeRust(t *testing.T) {
	sqliteHome := `C:\managed\sqlite`
	logDir := `C:\managed\log`
	modelCatalog := `C:\managed\models.json`
	checkUpdates := false
	loginShell := false
	feedbackEnabled := false
	requirements := &ConfigRequirements{
		SQLiteHome:              &sqliteHome,
		LogDir:                  &logDir,
		ModelCatalogJSON:        &modelCatalog,
		CheckForUpdateOnStartup: &checkUpdates,
		AllowLoginShell:         &loginShell,
		Feedback:                &FeedbackRequirements{Enabled: &feedbackEnabled},
	}
	values := map[string]any{
		"sqlite_home":                 `C:\user\sqlite`,
		"log_dir":                     `C:\user\log`,
		"model_catalog_json":          `C:\user\models.json`,
		"check_for_update_on_startup": true,
		"allow_login_shell":           true,
		"feedback":                    map[string]any{"enabled": true},
	}
	warnings := StartupWarnings(values, requirements)
	want := []string{
		`Configured value for ` + "`sqlite_home`" + ` is overridden by the required value AbsolutePathBuf("C:\\managed\\sqlite") from managed requirements.`,
		`Configured value for ` + "`log_dir`" + ` is overridden by the required value AbsolutePathBuf("C:\\managed\\log") from managed requirements.`,
		`Configured value for ` + "`model_catalog_json`" + ` is overridden by the required value AbsolutePathBuf("C:\\managed\\models.json") from managed requirements.`,
		"Configured value for `check_for_update_on_startup` is overridden by the required value false from managed requirements.",
		"Configured value for `allow_login_shell` is overridden by the required value false from managed requirements.",
		"Configured values under `feedback` are overridden by requirements from managed requirements.",
	}
	if len(warnings) != len(want) {
		t.Fatalf("StartupWarnings() = %#v, want %#v", warnings, want)
	}
	for index, warning := range warnings {
		if warning != want[index] {
			t.Fatalf("warning[%d] = %q, want %q", index, warning, want[index])
		}
	}
	// Matching values stay quiet, and an absent configured value is not a
	// conflict (Rust only warns when the configured value is present).
	quiet := StartupWarnings(map[string]any{
		"sqlite_home":                 sqliteHome,
		"log_dir":                     logDir,
		"model_catalog_json":          modelCatalog,
		"check_for_update_on_startup": false,
		"allow_login_shell":           false,
		"feedback":                    map[string]any{"enabled": false},
	}, requirements)
	if len(quiet) != 0 {
		t.Fatalf("StartupWarnings() = %#v, want none", quiet)
	}
	if absent := StartupWarnings(map[string]any{}, requirements); len(absent) != 0 {
		t.Fatalf("StartupWarnings() = %#v, want none for absent configured values", absent)
	}
}

// TestConfigRequirementsWireExposesExactRequirementTail pins the app-server
// wire shape (Rust app-server-protocol ConfigRequirements) for the newly exposed
// managed fields.
func TestConfigRequirementsWireExposesExactRequirementTail(t *testing.T) {
	path := "/managed/models.json"
	enabled := false
	requirements := &ConfigRequirements{
		ModelCatalogJSON: &path,
		AllowLoginShell:  &enabled,
		Feedback:         &FeedbackRequirements{Enabled: &enabled},
	}
	data, err := json.Marshal(requirements)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded["modelCatalogJson"] != path {
		t.Fatalf("modelCatalogJson = %#v, want %q", decoded["modelCatalogJson"], path)
	}
	if decoded["allowLoginShell"] != false {
		t.Fatalf("allowLoginShell = %#v, want false", decoded["allowLoginShell"])
	}
	feedback, ok := decoded["feedback"].(map[string]any)
	if !ok || feedback["enabled"] != false {
		t.Fatalf("feedback = %#v, want { enabled: false }", decoded["feedback"])
	}
}
