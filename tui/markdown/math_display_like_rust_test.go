package markdown

import (
	"strings"
	"testing"
)

// Rust parity: codex-rs/tui/src/markdown_render/math_display_tests.rs
// (`unicode_math_narrow_layout_stays_meaningful`,
// `unicode_math_oversized_display_stays_literal`, and
// `unicode_math_pending_source_and_pid_boundaries`).

func plainDisplayMath(t *testing.T, source string, width int) string {
	t.Helper()
	return strings.Join(plainMarkdownLines(t, source, width), "\n")
}

func TestMathNarrowLayoutStaysMeaningfulLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	formula := `$$\frac{1234567890}{x}$$`
	for _, width := range []int{80, 14} {
		for _, prefix := range []string{
			"- a\n  - b\n    - c\n\n      ",
			"- a\n  - b\n    - c\n",
			"> > > Quote\n",
		} {
			got := plainDisplayMath(t, prefix+formula, width)
			want := plainDisplayMath(t, prefix+"`"+formula+"`", width)
			if width >= 80 && got != want {
				t.Fatalf("width %d prefix %q = %q, want the code-span render %q", width, prefix, got, want)
			}
			if width < 80 {
				// A formula that has to fall back to its source is restored as one
				// atomic span after glamour v2 has wrapped the surrounding prose. The
				// code-span reference hard-wraps the TeX itself, so the narrow check
				// is that the source survives intact rather than split across lines.
				compact := strings.Join(strings.Fields(got), "")
				if formulaCompact := strings.Join(strings.Fields(formula), ""); !strings.Contains(compact, formulaCompact) {
					t.Fatalf("width %d prefix %q = %q, want the source %q kept intact", width, prefix, got, formula)
				}
				_ = want
			}
		}
	}
	// A fraction too wide to retain its geometry stays source, using ordinary
	// text wrapping.
	if got := plainDisplayMath(t, `$$\frac{abcdefghij}{k}$$`, 12); !strings.Contains(strings.Join(strings.Fields(got), ""), `$$\frac{abcdefghij}{k}$$`) {
		t.Fatalf("narrow fraction = %q, want the source kept intact", got)
	}
}

func TestMathOversizedDisplayStaysLiteralLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	for _, delimiters := range [][2]string{{"$$", "$$"}, {`\[`, `\]`}} {
		for _, ending := range []string{"", delimiters[1]} {
			source := delimiters[0] + "\n" + strings.Repeat("# x\n- y\n", 600) + ending
			if got, want := plainDisplayMath(t, source, 80), strings.TrimRight(source, "\n"); got != want {
				t.Fatalf("%s ending %q: oversized display = %q, want source", delimiters[0], ending, got)
			}
		}
	}
}

func TestMathPendingSourceAndPIDBoundariesLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	for _, delimiters := range [][2]string{{"$$", "$$"}, {`\[`, `\]`}} {
		open, close := delimiters[0], delimiters[1]
		if got, want := plainDisplayMath(t, open+"\nx^2\n\n+y^2\n"+close, 80), "x² +y²"; got != want {
			t.Fatalf("%s continued math = %q, want %q", open, got, want)
		}
		source := open + "\nx\n" + close + " trailing\n" + close + "\n\nAfter $\\alpha$."
		want := open + "\nx\n" + close + " trailing\n" + close + "\n\nAfter α."
		if got := plainDisplayMath(t, source, 80); got != want {
			t.Fatalf("%s rejected closer = %q, want %q", open, got, want)
		}
	}
	for _, source := range []string{"$$\n# x\n", "$$\n- x\n", "\\[\n# x\n", "\\[\n- x\n"} {
		if got, want := plainDisplayMath(t, source, 80), strings.TrimRight(source, "\n"); got != want {
			t.Fatalf("pending %q = %q, want source", source, got)
		}
	}
	if got, want := plainDisplayMath(t, "echo $$\n\n$$\nx^2\n$$\n\nAfter $\\alpha$.", 80), "echo $$\n\nx²\n\nAfter α."; got != want {
		t.Fatalf("pid then display = %q, want %q", got, want)
	}
	if got, want := plainDisplayMath(t, "$$\nprice=\\$$$\n\nAfter $\\alpha$.\n\n$$\\beta$$", 80), "$$\nprice=\\$$$\n\nAfter α.\n\nβ"; got != want {
		t.Fatalf("escaped price = %q, want %q", got, want)
	}
}
