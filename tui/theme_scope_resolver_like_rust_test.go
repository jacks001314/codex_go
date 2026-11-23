package tui

import "testing"

// Rust #62840 builds one syntax highlighter and reuses it for every foreground
// lookup in MarkdownStyles. Go selects the theme once per ThemeScopeResolver,
// so every scope must resolve to the same colour a per-lookup resolution gives.
func TestThemeScopeResolverMatchesPerLookupLikeRust(t *testing.T) {
	scopes := []string{
		"markup.heading.1.markdown",
		"markup.heading.markdown",
		"markup.italic.markdown",
		"markup.bold.markdown",
		"markup.underline.link.markdown",
		"markup.quote.markdown",
		"markup.inline.raw.string.markdown",
		"markup.raw.inline.markdown",
		"markup.list.numbered.markdown",
		"markup.list.unnumbered.markdown",
	}
	for _, theme := range []string{"ada", "dracula", "nord", "catppuccin-mocha"} {
		resolver := NewThemeScopeResolver(theme)
		for _, scope := range scopes {
			want, wantOK := themeScopeForeground(theme, scope)
			got, gotOK := resolver.Foreground(scope)
			if gotOK != wantOK || (gotOK && !sameTerminalColor(got, want)) {
				t.Fatalf("%s %s = %#v,%v want %#v,%v", theme, scope, got, gotOK, want, wantOK)
			}
		}
	}
	if value := NewThemeScopeResolver("").ForegroundValue("markup.heading.markdown"); value != "" {
		t.Fatalf("empty theme resolver value = %q, want empty", value)
	}
}

// Rust #62840 stops rebuilding the syntax theme for each lookup; the bundled
// asset parse is immutable input, so one parse serves every caller.
func TestBundledThemeParsingIsReusedLikeRust(t *testing.T) {
	first := loadBundledTheme("ada")
	second := loadBundledTheme(" ADA ")
	if first == nil || first != second {
		t.Fatalf("bundled theme parse not reused: %p vs %p", first, second)
	}
}
