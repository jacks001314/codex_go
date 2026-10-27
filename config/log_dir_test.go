package config

import (
	"os"
	"path/filepath"
	"testing"
)

// `configured_log_dir` reports the key only when user configuration or managed
// requirements set it, and a required sqlite_home still wins (Rust #55978).
func TestConfiguredLogDirMatchesRust(t *testing.T) {
	home := t.TempDir()
	cfg, err := LoadEffectiveWithOptions(home, &EffectiveOptions{IncludeManagedConfig: true})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions: %v", err)
	}
	if path, ok := cfg.ConfiguredLogDir(); ok || path != "" {
		t.Fatalf("ConfiguredLogDir() without log_dir = %q, %v", path, ok)
	}
	if cfg.HasManagedSQLiteHomeRequirement() {
		t.Fatalf("HasManagedSQLiteHomeRequirement() without requirements")
	}

	logsDir := filepath.Join(home, "custom-log")
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("log_dir = \""+filepath.ToSlash(logsDir)+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadEffectiveWithOptions(home, &EffectiveOptions{IncludeManagedConfig: true})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions: %v", err)
	}
	if path, ok := cfg.ConfiguredLogDir(); !ok || filepath.Clean(path) != filepath.Clean(logsDir) {
		t.Fatalf("ConfiguredLogDir() = %q, %v", path, ok)
	}
	if cfg.HasManagedSQLiteHomeRequirement() {
		t.Fatalf("a user log_dir must not count as a managed sqlite_home requirement")
	}

	requiredLogs := filepath.Join(t.TempDir(), "required-log")
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"),
		[]byte("log_dir = \""+filepath.ToSlash(requiredLogs)+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadEffectiveWithOptions(home, &EffectiveOptions{IncludeManagedConfig: true})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions: %v", err)
	}
	if path, ok := cfg.ConfiguredLogDir(); !ok || filepath.Clean(path) != filepath.Clean(requiredLogs) {
		t.Fatalf("ConfiguredLogDir() from requirements = %q, %v", path, ok)
	}
	if cfg.HasManagedSQLiteHomeRequirement() {
		t.Fatalf("a required log_dir is not a required sqlite_home")
	}

	sqliteHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"),
		[]byte("log_dir = \""+filepath.ToSlash(requiredLogs)+"\"\nsqlite_home = \""+filepath.ToSlash(sqliteHome)+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadEffectiveWithOptions(home, &EffectiveOptions{IncludeManagedConfig: true})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions: %v", err)
	}
	if !cfg.HasManagedSQLiteHomeRequirement() {
		t.Fatalf("HasManagedSQLiteHomeRequirement() = false with a required sqlite_home")
	}
}
