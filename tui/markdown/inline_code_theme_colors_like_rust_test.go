package markdown

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust #46069 "Use syntax theme colors for inline code and file paths": inline
// code and the local file paths the renderer emits as code spans share the
// active syntax theme's Markdown raw-text foreground, keeping cyan when the theme
// styles neither scope (MarkdownStyles::for_theme in markdown_render.rs).
//
// The expected colours are the values the Rust snapshot records for the same
// themes (catppuccin-mocha #a6e3a1, catppuccin-latte #40a02b, ansi green).
func TestInlineCodeAndFilePathsFollowSyntaxThemeLikeRust(t *testing.T) {
	defer codextui.SetStdoutColorLevelForTest(codextui.ColorTrue)()

	const source = "Use `src/main.rs` and [src/lib.rs](./src/lib.rs)."
	cases := []struct {
		theme string
		want  string
	}{
		{theme: "catppuccin-mocha", want: "166;227;161"},
		{theme: "catppuccin-latte", want: "64;160;43"},
		{theme: "ansi", want: "32"}, // palette index 2 (green) in its 16-colour form
	}
	for _, testCase := range cases {
		value := codextui.ThemeScopeForegroundValue(testCase.theme, "markup.inline.raw.string.markdown", "markup.raw.inline.markdown")
		if value == "" {
			t.Fatalf("theme %q resolves no raw-inline foreground", testCase.theme)
		}
		if *codexMarkdownStyle().Code.Color == value {
			t.Fatalf("theme %q did not patch the code style", testCase.theme)
		}
		rendered, err := RenderWithThemeCwd(source, 80, testCase.theme, "/repo")
		if err != nil {
			t.Fatalf("theme %q: %v", testCase.theme, err)
		}
		prefix := "\x1b[" + codeSpanSGR(testCase.want)
		if want := prefix + "src/main.rs\x1b[0m"; !strings.Contains(rendered, want) {
			t.Fatalf("theme %q inline code = %q, want %q", testCase.theme, rendered, want)
		}
		// The local file path is emitted as a code span wrapped in an OSC-8
		// hyperlink, so the theme colour still opens the span.
		if want := prefix + "\x1b]8;;file:///repo/src/lib.rs\a./src/lib.rs"; !strings.Contains(rendered, want) {
			t.Fatalf("theme %q file path = %q, want %q", testCase.theme, rendered, want)
		}
	}
}

// A theme that styles neither raw-inline scope keeps Rust's cyan fallback.
func TestInlineCodeKeepsCyanFallbackWithoutThemeScopeLikeRust(t *testing.T) {
	defer codextui.SetStdoutColorLevelForTest(codextui.ColorTrue)()
	if value := codextui.ThemeScopeForegroundValue("", "markup.inline.raw.string.markdown", "markup.raw.inline.markdown"); value != "" {
		t.Fatalf("unthemed raw-inline foreground = %q, want none", value)
	}
	if value := codextui.ThemeScopeForegroundValue("dracula", "markup.inline.raw.string.markdown", "markup.raw.inline.markdown"); value != "" {
		t.Fatalf("dracula raw-inline foreground = %q, want none", value)
	}
	rendered, err := RenderWithThemeCwd("Use `src/main.rs`.", 80, "dracula", "/repo")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if want := "\x1b[36msrc/main.rs\x1b[0m"; !strings.Contains(rendered, want) {
		t.Fatalf("fallback inline code = %q, want %q", rendered, want)
	}
}

// Bat themes that leave the inline-code scope unstyled but style the raw-inline
// scope use that colour instead (Rust falls through the scope list).
func TestInlineCodeUsesRawInlineScopeWhenInlineCodeScopeIsUnstyled(t *testing.T) {
	defer codextui.SetStdoutColorLevelForTest(codextui.ColorTrue)()
	if value := codextui.ThemeScopeForegroundValue("nord", "markup.inline.raw.string.markdown", "markup.raw.inline.markdown"); value != "#8fbcbb" {
		t.Fatalf("nord raw-inline foreground = %q, want %q", value, "#8fbcbb")
	}
	rendered, err := RenderWithThemeCwd("Use `src/main.rs`.", 80, "nord", "/repo")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if want := "\x1b[38;2;143;188;187msrc/main.rs\x1b[0m"; !strings.Contains(rendered, want) {
		t.Fatalf("nord inline code = %q, want %q", rendered, want)
	}
}

// codeSpanSGR is the SGR parameter list glamour emits for a theme value at the
// pinned truecolor level: a palette index becomes its 16-colour sequence and an
// rgb value becomes truecolor.
func codeSpanSGR(value string) string {
	if strings.Contains(value, ";") {
		return "38;2;" + value + "m"
	}
	return value + "m"
}
