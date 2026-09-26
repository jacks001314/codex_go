package markdown

import (
	"strings"
	"testing"
)

// Rust parity: codex-rs/tui/src/markdown_render/math.rs's `MathMarkdown` source
// scan, exercised through the Go Markdown pipeline.
//
// Go renders Markdown with goldmark/glamour, so the scan works at the source
// level (like the long-URL protection): an admitted math span is replaced with a
// placeholder before rendering and restored to its rendered Unicode afterwards,
// while rejected expressions stay verbatim and code/link/HTML ranges keep the
// ordinary renderer.
//
// Two independent, pre-existing pipeline divergences from Rust's line model are
// recorded separately rather than asserted here: glamour reflows a paragraph and
// drops its soft line breaks (Rust pushes a new line per soft break), and Go's
// web-link label annotation mangles a label that also occurs inside the URL.
// Both affect every Markdown render, not just math.

// TestMathSpansMaskLikeRust checks the source scan against Rust's snapshot
// sources: every admitted span becomes a placeholder, the rejected prose and
// shell dollars stay verbatim, code spans, link destinations and HTML stay
// untouched.
func TestMathSpansMaskLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	source := "Inline $\\alpha^2 + \\beta_{10}$ on $\\mathbb{R}^n$.\nRoot $\\sqrt{x^2+y^2}$ and fraction $a/\\frac{b}{c}$.\nUnsupported: $\\unknown{x_y}$ and $\\frac{a}{b}^2$.\nAngles: <$x$>, <\\(\\alpha\\)>, and <$\\unknown{x}$>.\nCode: `$\\alpha$`; money: $5 and $10; shell: $HOME."
	placeholders, masked := protectMathSpans(source, 80)
	wantMasked := "Inline 8MATHPROT0END on 8MATHPROT1END.\nRoot 8MATHPROT2END and fraction 8MATHPROT3END.\nUnsupported: $\\unknown{x_y}$ and $\\frac{a}{b}^2$.\nAngles: <8MATHPROT4END>, <8MATHPROT5END>, and <$\\unknown{x}$>.\nCode: `$\\alpha$`; money: $5 and $10; shell: $HOME."
	if masked != wantMasked {
		t.Fatalf("masked = %q, want %q", masked, wantMasked)
	}
	wantValues := map[string]string{
		"8MATHPROT0END": "α² + β₁₀",
		"8MATHPROT1END": "ℝⁿ",
		"8MATHPROT2END": "√(x²+y²)",
		"8MATHPROT3END": "a/((b)/(c))",
		"8MATHPROT4END": "x",
		"8MATHPROT5END": "α",
	}
	if len(placeholders) != len(wantValues) {
		t.Fatalf("placeholders = %#v, want %#v", placeholders, wantValues)
	}
	for key, want := range wantValues {
		if placeholders[key] != want {
			t.Fatalf("placeholder %q = %q, want %q (all %#v)", key, placeholders[key], want, placeholders)
		}
	}
}

// TestMathSpansProtectMarkdownContextsLikeRust pins the scanner's protected
// ranges: a dollar inside a link destination stays under the Markdown renderer.
func TestMathSpansProtectMarkdownContextsLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	source := "[x](https://example.com/$HOME) and $\\alpha$"
	placeholders, masked := protectMathSpans(source, 80)
	if len(placeholders) != 1 || placeholders["8MATHPROT0END"] != "α" {
		t.Fatalf("placeholders = %#v", placeholders)
	}
	if masked != "[x](https://example.com/$HOME) and 8MATHPROT0END" {
		t.Fatalf("masked = %q", masked)
	}
}

// TestMathRendersLikeRustContexts renders the single-line contexts from Rust's
// `unicode_math_preserves_markdown_contexts` through the whole pipeline.
func TestMathRendersLikeRustContexts(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	cases := []struct {
		name   string
		source string
		want   string
	}{
		{name: "markdown contexts", source: "**$\\alpha_1$** &amp; \\$5.00 and $\\beta^2$", want: "α₁ & $5.00 and β²"},
		{name: "code span and shell dollars", source: "`$\\alpha$` $HOME ${HOME} $(echo x) $5 and $10", want: "$\\alpha$ $HOME ${HOME} $(echo x) $5 and $10"},
		{name: "delimiters", source: "$\\left. x \\right|$ $\\text{x_y^z}$", want: " x | x_y^z"},
		{name: "paren delimiters", source: "\\(\\alpha^2\\)", want: "α²"},
		{name: "angles", source: "Value: <$x$> and <\\(\\alpha\\)>.", want: "Value: <x> and <α>."},
		{name: "initialism", source: "$USD$+$\\alpha$", want: "$USD$+α"},
		{name: "money and math", source: "Costs $5; $\\alpha$ and $x^2$.", want: "Costs $5; α and x²."},
		{name: "shell then math", source: "$HOME then $\\alpha$", want: "$HOME then α"},
		{name: "pid then math", source: "Run echo $$ to print PID. Then $\\alpha$", want: "Run echo $$ to print PID. Then α"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := plainMarkdownLines(t, testCase.source, 80)
			if strings.Join(got, "\n") != testCase.want {
				t.Fatalf("rendered = %#v, want %q", got, testCase.want)
			}
		})
	}
}

// TestMathRendersSnapshotContentLikeRust checks Rust's multi-line snapshots for
// the substitutions and verbatim restorations that do not depend on the source's
// line structure (see the file comment for the recorded soft-break divergence).
func TestMathRendersSnapshotContentLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	inline := "Inline $\\alpha^2 + \\beta_{10}$ on $\\mathbb{R}^n$.\nRoot $\\sqrt{x^2+y^2}$ and fraction $a/\\frac{b}{c}$.\nUnsupported: $\\unknown{x_y}$ and $\\frac{a}{b}^2$.\nAngles: <$x$>, <\\(\\alpha\\)>, and <$\\unknown{x}$>.\nCode: `$\\alpha$`; money: $5 and $10; shell: $HOME."
	joined := strings.Join(plainMarkdownLines(t, inline, 80), "\n")
	for _, want := range []string{
		"Inline α² + β₁₀ on ℝⁿ.",
		"Root √(x²+y²) and fraction a/((b)/(c)).",
		"Unsupported: $\\unknown{x_y}$ and $\\frac{a}{b}^2$.",
		"Angles: <x>, <α>, and <$\\unknown{x}$>.",
		"Code: $\\alpha$; money: $5 and $10; shell: $HOME.",
	} {
		if !containsCollapsedSpace(joined, want) {
			t.Fatalf("rendered %q missing %q", joined, want)
		}
	}

	grouped := "Indices: \\(x_{gpt} + x_{i,j} + w_{\\pi_{gt}} + x^{q+1} + x_{g}^2\\).\nNative scripts: \\(x_i^2 + y_{10}\\). Sets: \\(\\mathcal Q_t\\).\nAnnotations: \\(\\underset{i\\in I}{\\min} x_i\\), \\(\\overset{\\text{def}}{=}\\)."
	joined = strings.Join(plainMarkdownLines(t, grouped, 80), "\n")
	for _, want := range []string{
		"Indices: x_{gpt} + x_{i,j} + w_{π_{gt}} + x^{q+1} + x_{g}².",
		"Native scripts: xᵢ² + y₁₀. Sets: 𝒬ₜ.",
		"Annotations: (min)_{i∈ I} xᵢ, (=)^{def}.",
	} {
		if !containsCollapsedSpace(joined, want) {
			t.Fatalf("rendered %q missing %q", joined, want)
		}
	}
}

// containsCollapsedSpace compares text ignoring whitespace runs, since glamour
// reflows paragraphs at the render width.
func containsCollapsedSpace(haystack string, needle string) bool {
	return strings.Contains(
		strings.Join(strings.Fields(haystack), " "),
		strings.Join(strings.Fields(needle), " "),
	)
}

// A disabled math renderer keeps every span as source.
func TestMathPreferenceGateLikeRust(t *testing.T) {
	InitRendering(Rendering{Mermaid: true, Math: false, Tables: true, Lists: true})
	t.Cleanup(func() { InitRendering(DefaultRendering()) })
	got := plainMarkdownLines(t, "Inline $\\alpha^2$ and $\\beta$.", 80)
	if strings.Join(got, "\n") != "Inline $\\alpha^2$ and $\\beta$." {
		t.Fatalf("disabled math rendered = %#v", got)
	}
}

// Mirrors Rust's `unicode_math_pending_display_tracks_original_offset`: an
// unterminated standalone display records the start of its own line as
// `pending_start` (so a streamed preview keeps the region mutable), while a
// fenced code block, a prose-prefixed `$$` and an over-budget tail do not.
func TestMathPendingDisplayTracksOriginalOffsetLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	cases := []struct {
		name   string
		source string
		want   int
		set    bool
	}{
		{name: "standalone display", source: "Prose\n\n$$\nx^2\n\n", want: 7, set: true},
		{name: "fenced code block", source: "```\n$$\n"},
		{name: "shell pid", source: "echo $$\n"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			scan := analyzeMathSpans(testCase.source, 80)
			if testCase.set {
				if scan.pendingStart == nil || *scan.pendingStart != testCase.want {
					t.Fatalf("pendingStart = %v, want %d", scan.pendingStart, testCase.want)
				}
			} else if scan.pendingStart != nil {
				t.Fatalf("pendingStart = %d, want none", *scan.pendingStart)
			}
		})
	}

	// An over-budget tail is left to the ordinary renderer, so no pending region
	// is tracked.
	oversized := "$$\n" + strings.Repeat("x", maxMathBytes+1)
	if scan := analyzeMathSpans(oversized, 80); scan.pendingStart != nil {
		t.Fatalf("oversized pending display = %d, want none", *scan.pendingStart)
	}
}
