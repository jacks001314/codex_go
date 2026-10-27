package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUnsafeEnforceSQLiteJournalModeRequirementLikeRust mirrors Rust #57225:
// the managed `unsafe_enforce_sqlite_journal_mode` requirement overrides the
// configured value and reaches the effective config that builds SqliteConfig.
func TestUnsafeEnforceSQLiteJournalModeRequirementLikeRust(t *testing.T) {
	write := func(t *testing.T, path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	load := func(t *testing.T, configTOML, requirementsTOML string) *Config {
		t.Helper()
		home := t.TempDir()
		write(t, ConfigPath(home), configTOML)
		write(t, filepath.Join(home, "requirements.toml"), requirementsTOML)
		cfg, err := LoadEffective(home, nil, nil, nil)
		if err != nil {
			t.Fatalf("LoadEffective() error = %v", err)
		}
		return cfg
	}

	enforced := load(t, "unsafe_enforce_sqlite_journal_mode = false\n", "unsafe_enforce_sqlite_journal_mode = true\n")
	if !enforced.UnsafeEnforceSQLiteJournalMode() {
		t.Fatal("managed requirement true must enable NFS DELETE enforcement")
	}

	disabled := load(t, "unsafe_enforce_sqlite_journal_mode = true\n", "unsafe_enforce_sqlite_journal_mode = false\n")
	if disabled.UnsafeEnforceSQLiteJournalMode() {
		t.Fatal("managed requirement false must disable NFS DELETE enforcement")
	}

	unset := load(t, "", "")
	if unset.UnsafeEnforceSQLiteJournalMode() {
		t.Fatal("enforcement must stay off without a requirement")
	}
}

// TestParseUnsafeEnforceSQLiteJournalModeRequirement pins the snake_case and
// camelCase requirement spellings used by requirements.toml and cloud config.
func TestParseUnsafeEnforceSQLiteJournalModeRequirement(t *testing.T) {
	for _, key := range []string{"unsafe_enforce_sqlite_journal_mode", "unsafeEnforceSqliteJournalMode"} {
		requirements := ConfigRequirementsFromMap(map[string]any{key: true})
		if requirements == nil || requirements.UnsafeEnforceSQLiteJournalMode == nil || !*requirements.UnsafeEnforceSQLiteJournalMode {
			t.Fatalf("%s requirement = %#v", key, requirements)
		}
	}
}
