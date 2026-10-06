package appserverdaemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func envLookup(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return entry[len(prefix):], true
		}
	}
	return "", false
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("Abs(%q): %v", path, err)
	}
	return abs
}

// TestSetWorkingDirectoryAbsolutizesProcessPathsLikeRust mirrors Rust's
// background_command::set_working_directory (#49819): process-scoped paths are
// made absolute before a child changes cwd, while home-relative values that
// consumers expand themselves are preserved verbatim.
func TestSetWorkingDirectoryAbsolutizesProcessPathsLikeRust(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("SSL_CERT_FILE", filepath.Join("certs", "ca.pem"))
	t.Setenv("CODEX_CA_CERTIFICATE", "ca.pem")
	t.Setenv("npm_config_cafile", "~/npm.pem")
	t.Setenv("SSL_CERT_DIR", strings.Join([]string{"a", "b"}, string(os.PathListSeparator)))
	t.Setenv("CODEX_SQLITE_HOME", "  sqlite-data  ")

	command := exec.Command("echo")
	if err := setWorkingDirectory(command, stateDir); err != nil {
		t.Fatalf("setWorkingDirectory: %v", err)
	}

	if got, _ := envLookup(command.Env, "SSL_CERT_FILE"); got != mustAbs(t, filepath.Join("certs", "ca.pem")) {
		t.Fatalf("SSL_CERT_FILE = %q, want absolute", got)
	}
	if got, _ := envLookup(command.Env, "CODEX_CA_CERTIFICATE"); got != mustAbs(t, "ca.pem") {
		t.Fatalf("CODEX_CA_CERTIFICATE = %q, want absolute", got)
	}
	// Home-relative npm cafile is preserved for the consumer's own expansion.
	if got, _ := envLookup(command.Env, "npm_config_cafile"); got != "~/npm.pem" {
		t.Fatalf("npm_config_cafile = %q, want unchanged", got)
	}
	wantDir := strings.Join([]string{mustAbs(t, "a"), mustAbs(t, "b")}, string(os.PathListSeparator))
	if got, _ := envLookup(command.Env, "SSL_CERT_DIR"); got != wantDir {
		t.Fatalf("SSL_CERT_DIR = %q, want %q", got, wantDir)
	}
	if runtime.GOOS == "windows" {
		// CODEX_SQLITE_HOME is Windows-only: trimmed and made absolute.
		if got, _ := envLookup(command.Env, "CODEX_SQLITE_HOME"); got != mustAbs(t, "sqlite-data") {
			t.Fatalf("CODEX_SQLITE_HOME = %q, want absolute", got)
		}
		if command.Dir != stateDir {
			t.Fatalf("Dir = %q, want %q", command.Dir, stateDir)
		}
	}
}
