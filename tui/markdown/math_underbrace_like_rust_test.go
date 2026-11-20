package markdown

import (
	"strings"
	"testing"
)

// Rust parity: codex-rs/tui/src/markdown_render/math_display_tests.rs
// (`unicode_math_underbrace`, #62577) and its insta snapshot. The brace spans the
// expression's terminal columns, the label centers beneath it, and surrounding
// math stays aligned with the expression's baseline.
func TestMathUnderbraceSnapshotLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	source := "\\[\n" +
		"\\varphi=\\underbrace{(x\\lor y\\lor z)}_{C_1}\\land\\underbrace{(\\neg x\\lor y)}_{C_2}\n" +
		"\\]\n\n" +
		"$$\\underbrace{x}_{\\text{term}}+\\underbrace{xy}_{C_3}+\\underbrace{x+y}_{C_4}$$\n\n" +
		"$$\\underbrace{a+bc}_{\\text{total}}$$"
	want := strings.Join([]string{
		"\u03c6=(x\u2228 y\u2228 z)\u2227 (\u00ac x\u2228 y)",
		"  \u2514\u2500\u2500\u2500\u252c\u2500\u2500\u2500\u2518  \u2514\u2500\u2500\u252c\u2500\u2500\u2500\u2518",
		"      C\u2081        C\u2082",
		"",
		" x  + xy+ x+y",
		" \u252c    \u2514\u2518  \u2514\u252c\u2518",
		"term  C\u2083   C\u2084",
		"",
		" a+bc",
		" \u2514\u252c\u2500\u2518",
		"total",
	}, "\n")
	if got := plainDisplayMath(t, source, 80); got != want {
		t.Fatalf("underbrace snapshot =\n%s\nwant\n%s", got, want)
	}
}

// Rust parity: the same test's fallback cases. An unsupported argument, a
// missing or unbraced label, a nested underbrace and inline math all keep the
// existing source fallback.
func TestMathUnderbraceFallsBackLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	for _, source := range []string{
		`\underbrace{x+y}`,
		`\underbrace{x+y}_C`,
		`\underbrace{}_{C}`,
		`\underbrace{x}_{}`,
		`\underbrace{\frac{x}{y}}_{C}`,
		`\underbrace{x}_{\frac{a}{b}}`,
		`\underbrace{\underbrace{x}_{a}}_{b}`,
	} {
		if _, ok := mathRenderLayout(source, mathModeDisplay); ok {
			t.Fatalf("%s rendered, want the source fallback", source)
		}
	}
	if _, ok := mathRenderLayout(`\underbrace{x}_{C}`, mathModeInline); ok {
		t.Fatal("inline \\underbrace rendered, want the source fallback")
	}

	// A formula that has to fall back is restored as one atomic span after the
	// surrounding prose is wrapped, so the source survives intact instead of
	// being hard-wrapped mid-expression.
	formula := `$$\underbrace{abcdefghij}_{C_1}$$`
	compact := strings.Join(strings.Fields(plainDisplayMath(t, formula, 12)), "")
	if formulaCompact := strings.Join(strings.Fields(formula), ""); !strings.Contains(compact, formulaCompact) {
		t.Fatalf("over-width underbrace = %q, want the source %q kept intact", compact, formula)
	}
}
