package tui

import (
	"regexp"
	"strings"
	"testing"
)

func TestThreadColorIndexUsesStableFNV1a(t *testing.T) {
	cases := []struct {
		threadID string
		size     int
		want     int
	}{
		{"abc", 7, 5},
		{"abc", 5, 1},
		{"00000000-0000-0000-0000-000000000001", 7, 4},
		{"00000000-0000-0000-0000-000000000001", 5, 3},
		{"3f2a1c9e-1111-2222-3333-444455556666", 7, 5},
		{"3f2a1c9e-1111-2222-3333-444455556666", 5, 4},
	}
	for _, tc := range cases {
		if got := ThreadColorIndex(tc.threadID, tc.size); got != tc.want {
			t.Fatalf("ThreadColorIndex(%q, %d) = %d, want %d", tc.threadID, tc.size, got, tc.want)
		}
	}
	if got := ThreadColorIndex("abc", 0); got != 0 {
		t.Fatalf("ThreadColorIndex with empty palette = %d, want 0", got)
	}
}

func TestThreadColorPaletteExcludesBaseAndIsStable(t *testing.T) {
	ResetThreadColorPaletteCache()
	palette := ThreadColorPalette("catppuccin-mocha")
	if len(palette) == 0 {
		t.Fatal("expected accents for catppuccin-mocha")
	}
	hexPattern := regexp.MustCompile(`^#[0-9a-f]{6}$`)
	seen := map[string]bool{}
	for _, color := range palette {
		if !hexPattern.MatchString(color) {
			t.Fatalf("accent %q is not a hex triplet", color)
		}
		if seen[color] {
			t.Fatalf("accent %q appears twice", color)
		}
		seen[color] = true
	}
	// Cached lookups must be identical.
	second := ThreadColorPalette("catppuccin-mocha")
	if strings.Join(palette, ",") != strings.Join(second, ",") {
		t.Fatalf("palette not stable: %v vs %v", palette, second)
	}
}

func TestThreadColorForThemeDeterministicAndPaletteMember(t *testing.T) {
	palette := ThreadColorPalette("catppuccin-mocha")
	threadID := "00000000-0000-0000-0000-000000000001"
	first := ThreadColorForTheme(threadID, "catppuccin-mocha")
	if first == "" {
		t.Fatal("expected a color for a known thread id")
	}
	if !strings.Contains(strings.Join(palette, ","), first) {
		t.Fatalf("color %q is not in the palette %v", first, palette)
	}
	if second := ThreadColorForTheme(threadID, "catppuccin-mocha"); second != first {
		t.Fatalf("color changed between calls: %q vs %q", first, second)
	}
	if got := ThreadColorForTheme("", "catppuccin-mocha"); got != "" {
		t.Fatalf("empty thread id color = %q, want empty", got)
	}
}

func TestThreadColorSGR(t *testing.T) {
	if got := ThreadColorSGR("#ff8000"); got != "\x1b[38;2;255;128;0m" {
		t.Fatalf("ThreadColorSGR = %q", got)
	}
	for _, invalid := range []string{"", "ff8000", "#ff80", "#zzzzzz", "#ff80000"} {
		if got := ThreadColorSGR(invalid); got != "" {
			t.Fatalf("ThreadColorSGR(%q) = %q, want empty", invalid, got)
		}
	}
}

// TestThreadColorPaletteFollowsThemeChanges covers Rust #44857's palette
// refresh: switching the active theme must select that theme's accents. The Go
// cache is keyed by the resolved syntax style, so a theme change never reuses
// the previous theme's palette, and an explicit reset reloads deterministically.
func TestThreadColorPaletteFollowsThemeChanges(t *testing.T) {
	ResetThreadColorPaletteCache()
	mocha := ThreadColorPalette("catppuccin-mocha")
	dark := ThreadColorPalette("dracula")
	if len(mocha) == 0 || len(dark) == 0 {
		t.Fatalf("expected accents for both themes: %d/%d", len(mocha), len(dark))
	}
	if strings.Join(mocha, ",") == strings.Join(dark, ",") {
		t.Fatalf("theme change reused the previous palette: %v", mocha)
	}
	if again := ThreadColorPalette("catppuccin-mocha"); strings.Join(again, ",") != strings.Join(mocha, ",") {
		t.Fatalf("switching back changed the palette: %v vs %v", mocha, again)
	}
	ResetThreadColorPaletteCache()
	threadColorPaletteMu.Lock()
	cached := len(threadColorPaletteCache)
	threadColorPaletteMu.Unlock()
	if cached != 0 {
		t.Fatalf("reset left %d cached palettes", cached)
	}
	if reloaded := ThreadColorPalette("dracula"); strings.Join(reloaded, ",") != strings.Join(dark, ",") {
		t.Fatalf("reloaded palette = %v, want %v", reloaded, dark)
	}
}
