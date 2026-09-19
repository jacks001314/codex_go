
package tui

import (
	"slices"
	"testing"

	chromastyles "github.com/alecthomas/chroma/v2/styles"
)

// Mirrors Rust #46504: the six bundled TextMate themes are listed in the theme
// picker, highlight through their own colours, and declare a `codex.accent`.
func TestBundledThemesLikeRust(t *testing.T) {
	bundled := []string{"ada", "babbage", "curie", "cushman", "dali", "davinci"}
	ids := BuiltinThemeIDs()
	for _, id := range bundled {
		if !slices.Contains(ids, id) {
			t.Fatalf("builtin theme %q missing from the picker list", id)
		}
		style := ChromaThemeForCodexTheme(id)
		if style == "" || style == "catppuccin-mocha" {
			t.Fatalf("theme %q resolved to %q, want its bundled style", id, style)
		}
		if chromastyles.Get(style) == nil {
			t.Fatalf("theme %q style %q is not registered", id, style)
		}
		if accent := BundledThemeAccent(id); accent == "" {
			t.Fatalf("theme %q does not declare a codex.accent", id)
		}
	}
	if accent := BundledThemeAccent("ada"); accent != "#5FAFFF" {
		t.Fatalf("ada accent = %q, want #5FAFFF", accent)
	}
	if accent := BundledThemeAccent("not-a-bundled-theme"); accent != "" {
		t.Fatalf("unknown theme accent = %q, want empty", accent)
	}
}
