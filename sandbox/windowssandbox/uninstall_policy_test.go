package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func writeUninstallConfig(t *testing.T, home string, contents string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func TestCleanUninstallEnabledDefaultsToPreinstalledWhenConfigIsMissing(t *testing.T) {
	home := t.TempDir()
	if !CleanUninstallEnabled(home, true) {
		t.Fatal("missing config must keep the preinstall default")
	}
	if CleanUninstallEnabled(home, false) {
		t.Fatal("missing config must keep the non-preinstall default")
	}
}

func TestCleanUninstallEnabledReadsDesktopPreference(t *testing.T) {
	home := t.TempDir()
	writeUninstallConfig(t, home, "[desktop]\nuninstall_codex_home = true\n")
	if !CleanUninstallEnabled(home, false) {
		t.Fatal("explicit true must enable cleanup")
	}
	writeUninstallConfig(t, home, "[desktop]\nuninstall_codex_home = false\n")
	if CleanUninstallEnabled(home, true) {
		t.Fatal("explicit false must disable cleanup")
	}
}

func TestCleanUninstallEnabledDefaultsWhenKeyIsAbsentOrInvalid(t *testing.T) {
	home := t.TempDir()
	writeUninstallConfig(t, home, "[desktop]\nfeatures = { multi_agent_v2 = true }\n")
	if !CleanUninstallEnabled(home, true) || CleanUninstallEnabled(home, false) {
		t.Fatal("a missing preference must keep the preinstall default")
	}
	writeUninstallConfig(t, home, "[desktop]\nuninstall_codex_home = \"yes\"\n")
	if CleanUninstallEnabled(home, true) {
		t.Fatal("a non-boolean preference must disable cleanup")
	}
	writeUninstallConfig(t, home, "this is not toml = = =\n")
	if CleanUninstallEnabled(home, true) {
		t.Fatal("invalid TOML must disable cleanup")
	}
}
