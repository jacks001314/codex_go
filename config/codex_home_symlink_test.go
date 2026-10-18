package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAllowedSymlinkedCodexHomeUsesTopLevelUserConfigLikeRust freezes Rust
// #42716: only a boolean in the host's top-level user config enables the
// Seatbelt writable-root exception.
func TestAllowedSymlinkedCodexHomeUsesTopLevelUserConfigLikeRust(t *testing.T) {
	home := canonicalTestPath(t, t.TempDir())
	writeCodexHomeConfig(t, filepath.Join(home, "config.toml"), "allow_symlinked_codex_home = true\n")
	t.Setenv("GCODE_HOME", "")
	t.Setenv("CODEX_HOME", "")

	cfg, err := LoadEffectiveWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
	}
	if got := cfg.AllowedSymlinkedCodexHome(); got != home {
		t.Fatalf("AllowedSymlinkedCodexHome() = %q, want %q", got, home)
	}
	if !cfg.AllowSymlinkedCodexHome() {
		t.Fatal("AllowSymlinkedCodexHome() = false, want the user-config opt-in")
	}

	project := filepath.Join(home, "project")
	if err := os.MkdirAll(filepath.Join(project, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCodexHomeConfig(t, filepath.Join(project, ".codex", "config.toml"), "allow_symlinked_codex_home = true\n")
	writeCodexHomeConfig(t, filepath.Join(home, "config.toml"), "allow_symlinked_codex_home = false\n")
	cfg, err = LoadEffectiveWithOptions(home, &EffectiveOptions{
		CWD:          project,
		RawOverrides: []string{"allow_symlinked_codex_home=true"},
	})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions(disabled) error = %v", err)
	}
	if got := cfg.AllowedSymlinkedCodexHome(); got != "" {
		t.Fatalf("project/CLI opt-in enabled the exception: %q", got)
	}
	if cfg.AllowSymlinkedCodexHome() {
		t.Fatal("project/CLI override enabled AllowSymlinkedCodexHome")
	}
}

// TestAllowedSymlinkedCodexHomePreservesOnlyMatchingAliasLikeRust mirrors
// the alias rule: a CODEX_HOME spelling is retained only when it resolves to
// the loaded home; a spelling that traverses a symlink with `..` is replaced by
// the canonical home.
func TestAllowedSymlinkedCodexHomePreservesOnlyMatchingAliasLikeRust(t *testing.T) {
	base := canonicalTestPath(t, t.TempDir())
	home := filepath.Join(base, "home")
	child := filepath.Join(base, "child")
	for _, dir := range []string{home, child} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeCodexHomeConfig(t, filepath.Join(home, "config.toml"), "allow_symlinked_codex_home = true\n")
	alias := filepath.Join(base, "user-home")
	if err := os.Symlink(home, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	homeLink := filepath.Join(base, "home-link")
	if err := os.Symlink(child, homeLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	t.Setenv("GCODE_HOME", "")
	t.Setenv("CODEX_HOME", alias)
	cfg, err := LoadEffectiveWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions(alias) error = %v", err)
	}
	if got := cfg.AllowedSymlinkedCodexHome(); got != alias {
		t.Fatalf("matching alias = %q, want %q", got, alias)
	}

	// `home-link/..` is a lexical path whose canonical target is base, then
	// home. It must not be retained as an alias for the loaded home.
	lexicalAlias := filepath.Join(homeLink, "..", "home")
	t.Setenv("CODEX_HOME", lexicalAlias)
	cfg, err = LoadEffectiveWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions(lexical alias) error = %v", err)
	}
	if got := cfg.AllowedSymlinkedCodexHome(); got != home {
		t.Fatalf("lexical alias = %q, want canonical home %q", got, home)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	return filepath.Clean(resolved)
}

func writeCodexHomeConfig(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
