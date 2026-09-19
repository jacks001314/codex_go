package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDiffBackgroundTheme(t *testing.T, name string, insertedBackground string, deletedBackground string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("GCODE_HOME", home)
	themeDir := DefaultThemeDir()
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", themeDir, err)
	}
	var scopes strings.Builder
	appendScope := func(scope string, background string) {
		if background == "" {
			return
		}
		scopes.WriteString("    <dict><key>scope</key><string>" + scope + "</string>\n")
		scopes.WriteString("      <key>settings</key><dict><key>background</key><string>" + background + "</string></dict></dict>\n")
	}
	appendScope("markup.inserted", insertedBackground)
	appendScope("markup.deleted", deletedBackground)
	body := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
		"<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n" +
		"<plist version=\"1.0\">\n<dict>\n  <key>name</key><string>" + name + "</string>\n" +
		"  <key>settings</key>\n  <array>\n" +
		"    <dict><key>settings</key><dict><key>foreground</key><string>#D0D0D0</string></dict></dict>\n" +
		scopes.String() +
		"  </array>\n</dict>\n</plist>\n"
	if err := os.WriteFile(filepath.Join(themeDir, name+".tmTheme"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(theme) error = %v", err)
	}
	return name
}

// Mirrors Rust #46504 resolve_diff_backgrounds_for: the hardcoded palette is the
// baseline and a theme scope background overrides it at rich colour levels.
func TestResolveDiffLineBackgroundLikeRust(t *testing.T) {
	// A theme that declares no diff scope background keeps the palette baseline.
	if got := resolveDiffLineBackground("", DiffLineInsert, false, ColorTrue); got != ansiBgAddDark {
		t.Fatalf("theme without a diff background = %q, want the fallback %q", got, ansiBgAddDark)
	}

	// The bundled dali/davinci themes disable both fills.
	for _, theme := range []string{"dali", "davinci"} {
		if got := resolveDiffLineBackground(theme, DiffLineInsert, false, ColorTrue); got != "" {
			t.Fatalf("%s insert fill = %q, want none", theme, got)
		}
		if got := resolveDiffLineBackground(theme, DiffLineDelete, false, ColorTrue); got != "" {
			t.Fatalf("%s delete fill = %q, want none", theme, got)
		}
	}

	name := writeDiffBackgroundTheme(t, "diff-background-test", "#112233", "#00000001")
	if got := resolveDiffLineBackground(name, DiffLineInsert, false, ColorTrue); got != "\x1b[48;2;17;34;51m" {
		t.Fatalf("truecolor theme fill = %q", got)
	}
	if got := resolveDiffLineBackground(name, DiffLineInsert, false, ColorANSI256); !strings.HasPrefix(got, "\x1b[48;5;") {
		t.Fatalf("256-colour theme fill = %q", got)
	}
	// Below the rich levels the fallback palette wins, matching Rust.
	if got := resolveDiffLineBackground(name, DiffLineInsert, false, ColorANSI16); got != "" {
		t.Fatalf("ANSI-16 theme fill = %q, want none", got)
	}
	// The explicit terminal-default marker clears only its own scope.
	if got := resolveDiffLineBackground(name, DiffLineDelete, false, ColorTrue); got != "" {
		t.Fatalf("terminal-default delete fill = %q, want none", got)
	}
}

// Mirrors Rust #46504's gutter rule: a line without a background gives its
// gutter no background either.
func TestDiffGutterClearsFillLikeRust(t *testing.T) {
	if prefix := buildDiffPrefix(DiffLineInsert, "1", "+", true, ColorTrue, "dali"); strings.Contains(prefix, "48;") {
		t.Fatalf("gutter kept a fill for a cleared line: %q", prefix)
	}
	fallback := buildDiffPrefix(DiffLineInsert, "1", "+", true, ColorTrue, "")
	if !strings.Contains(fallback, "48;") {
		t.Fatalf("gutter dropped the fallback fill: %q", fallback)
	}
	// A theme fill keeps the gutter's own tinted cell.
	name := writeDiffBackgroundTheme(t, "diff-background-gutter-test", "#112233", "")
	if prefix := buildDiffPrefix(DiffLineInsert, "1", "+", true, ColorTrue, name); !strings.Contains(prefix, "48;") {
		t.Fatalf("gutter lost its fill for a themed line: %q", prefix)
	}
	// The terminal-default marker clears that scope's gutter fill.
	cleared := writeDiffBackgroundTheme(t, "diff-background-gutter-cleared-test", "#00000001", "")
	if prefix := buildDiffPrefix(DiffLineInsert, "1", "+", true, ColorTrue, cleared); strings.Contains(prefix, "48;") {
		t.Fatalf("gutter kept a fill for a scope without one: %q", prefix)
	}
}
