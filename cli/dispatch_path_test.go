package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust's arg0 launcher puts the package's own `codex-path` directory on PATH
// before the alias directory, so a packaged install exposes its bundled command
// shims to every child process while the arg0 aliases still win.
func TestArg0UpdatedPATHIncludesThePackagePathLikeRust(t *testing.T) {
	packageDir := t.TempDir()
	binDir := filepath.Join(packageDir, "bin")
	pathDir := filepath.Join(packageDir, "codex-path")
	for _, dir := range []string{binDir, pathDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(packageDir, "codex-package.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write package metadata error = %v", err)
	}
	exe := filepath.Join(binDir, "codex")
	if err := os.WriteFile(exe, []byte(""), 0o600); err != nil {
		t.Fatalf("write exe error = %v", err)
	}
	aliasDir := filepath.Join(t.TempDir(), "codex-arg0")
	updated := arg0UpdatedPATH(aliasDir, exe, "/usr/bin"+string(os.PathListSeparator)+"/bin")
	entries := strings.Split(updated, string(os.PathListSeparator))
	if len(entries) != 4 || entries[0] != aliasDir || entries[1] != pathDir {
		t.Fatalf("PATH = %q", updated)
	}

	// An install without a package layout keeps just the alias directory.
	plainExe := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(plainExe, []byte(""), 0o600); err != nil {
		t.Fatalf("write exe error = %v", err)
	}
	plain := arg0UpdatedPATH(aliasDir, plainExe, "/usr/bin")
	if plain != PathEnvWithEntry(aliasDir, "/usr/bin") {
		t.Fatalf("PATH = %q", plain)
	}
}
