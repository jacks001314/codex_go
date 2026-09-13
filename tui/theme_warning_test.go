package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// Mirrors Rust's validate_theme_name: bundled themes and loadable custom theme
// files are accepted, an existing but invalid `.tmTheme` reports the
// could-not-be-loaded warning, an unknown name reports the not-found warning,
// and an unset theme is silent.
func TestThemeStartupWarningLikeRust(t *testing.T) {
	home := t.TempDir()
	if got := ThemeStartupWarning("", home); got != "" {
		t.Fatalf("ThemeStartupWarning(unset) = %q", got)
	}
	for _, bundled := range []string{"ansi", "catppuccin-mocha", "dracula", "zenburn"} {
		if got := ThemeStartupWarning(bundled, home); got != "" {
			t.Fatalf("ThemeStartupWarning(%q) = %q, want none", bundled, got)
		}
	}
	customPath := filepath.Join(home, "themes", "my-custom.tmTheme")
	if err := os.MkdirAll(filepath.Dir(customPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(customPath, []byte(minimalTMThemeContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ThemeStartupWarning("my-custom", home); got != "" {
		t.Fatalf("ThemeStartupWarning(custom file) = %q, want none", got)
	}
	if err := os.WriteFile(customPath, []byte("not a plist"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantInvalid := "Custom theme \"my-custom\" at " + customPath +
		" could not be loaded (invalid .tmTheme format). Falling back to the default theme."
	if got := ThemeStartupWarning("my-custom", home); got != wantInvalid {
		t.Fatalf("ThemeStartupWarning(invalid custom file) = %q, want %q", got, wantInvalid)
	}

	missingPath := filepath.Join(home, "themes", "bogus-theme.tmTheme")
	want := "Theme \"bogus-theme\" not found. Using the default theme. To use a custom theme, place a .tmTheme file at " + missingPath + "."
	if got := ThemeStartupWarning("bogus-theme", home); got != want {
		t.Fatalf("ThemeStartupWarning(unknown) = %q, want %q", got, want)
	}
	// Without a resolvable home the warning names the $CODEX_HOME form.
	wantNoHome := "Theme \"bogus-theme\" not found. Using the default theme. To use a custom theme, place a .tmTheme file at " + filepath.Join("$CODEX_HOME", "themes", "bogus-theme.tmTheme") + "."
	if got := ThemeStartupWarning("bogus-theme", ""); got != wantNoHome {
		t.Fatalf("ThemeStartupWarning(unknown, no home) = %q, want %q", got, wantNoHome)
	}
}
