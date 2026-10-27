package markdown

import (
	"strings"
	"testing"
)

// Rust #57393 (d0f33fab36, "Render integral limits and parenthesized matrices in
// TUI math"): top-level display integrals stack their limits like sums and
// wedges, while inline and nested display math keep the compact form.
func TestMathRenderIntegralLimitsLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   string
		inline string
	}{
		{
			name:   "infinite upper limit",
			source: `\int_{0}^{\infty} e^{-x}\,dx = 1`,
			inline: "∫₀^{∞} e⁻ˣ dx = 1",
			want: strings.Join([]string{
				"∞",
				"∫ e⁻ˣ dx = 1",
				"0",
			}, "\n"),
		},
		{
			name:   "fraction beside the integral",
			source: `\int_0^1 x^2\,dx=\frac{1}{3}`,
			inline: "∫₀¹ x² dx=((1)/(3))",
			want: strings.Join([]string{
				"1        1",
				"∫ x² dx= ─",
				"0        3",
			}, "\n"),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := mathRender(testCase.source, true)
			if !ok {
				t.Fatalf("mathRender(%q, true) rejected the integral", testCase.source)
			}
			if compact := compactLayout(got); compact != testCase.want {
				t.Fatalf("mathRender(%q, true) = %q, want %q", testCase.source, got, testCase.want)
			}
			inline, ok := mathRender(testCase.source, false)
			if !ok {
				t.Fatalf("mathRender(%q, false) rejected the integral", testCase.source)
			}
			if inline != testCase.inline {
				t.Fatalf("inline integral = %q, want %q", inline, testCase.inline)
			}
		})
	}
	// Nested display math (lists and blockquotes) keeps compact operator limits
	// even though its fractions still stack.
	nested, ok := mathRenderLayout(`\int_0^1 x\,dx`, mathModeNestedDisplay)
	if !ok || nested.text != "∫₀¹ x dx" || nested.hasRequiredWidth {
		t.Fatalf("nested display integral = %#v (ok=%v), want the compact row", nested, ok)
	}
}

// Rust #57393: `pmatrix` renders a centered grid inside scalable parentheses.
// A single row keeps round parentheses, the first and last rows use the corner
// glyphs, and the rest use the extenders.
func TestMathRenderParenthesizedMatricesLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "two rows",
			source: `\begin{pmatrix}1&2\\3&4\end{pmatrix}`,
			want: strings.Join([]string{
				"⎛ 1  2 ⎞",
				"⎝ 3  4 ⎠",
			}, "\n"),
		},
		{
			name:   "expression wrapper",
			source: `A=\begin{pmatrix}1&2\\3&4\end{pmatrix}`,
			want: strings.Join([]string{
				"A=⎛ 1  2 ⎞",
				"  ⎝ 3  4 ⎠",
			}, "\n"),
		},
		{
			name:   "single row uses round parentheses",
			source: `v=\begin{pmatrix}10&20&30\end{pmatrix}`,
			want:   "v=( 10  20  30 )",
		},
		{
			name:   "cells center around fractions",
			source: `\begin{pmatrix}\frac{1}{2}&0\\*100&x\end{pmatrix}v`,
			want: strings.Join([]string{
				"⎛  1     ⎞",
				"⎜  ─   0 ⎟v",
				"⎜  2     ⎟",
				"⎝ 100  x ⎠",
			}, "\n"),
		},
		{
			name:   "row options stay in cells",
			source: `\begin{pmatrix}a&b\\ *x&y\\ [2pt]&z\end{pmatrix}`,
			want: strings.Join([]string{
				"⎛   a    b ⎞",
				"⎜  *x    y ⎟",
				"⎝ [2pt]  z ⎠",
			}, "\n"),
		},
		{
			name:   "adjacent star belongs to the row break",
			source: `\begin{pmatrix}1&2\\*3&4\end{pmatrix}`,
			want: strings.Join([]string{
				"⎛ 1  2 ⎞",
				"⎝ 3  4 ⎠",
			}, "\n"),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := mathRender(testCase.source, true)
			if !ok {
				t.Fatalf("mathRender(%q, true) rejected the matrix", testCase.source)
			}
			if got != testCase.want {
				t.Fatalf("mathRender(%q, true) = %q, want %q", testCase.source, got, testCase.want)
			}
			if got, ok := mathRender(testCase.source, false); ok {
				t.Fatalf("inline matrix = %q, want rejected", got)
			}
		})
	}
}

// Rust #57393: matrices are spatial layouts. Every row must fill the header's
// column count and the environment must close itself.
func TestMathRenderMatrixRejectionsLikeRust(t *testing.T) {
	sources := []string{
		`\begin{pmatrix}1&2\\3\end{pmatrix}`,
		`\begin{pmatrix}1&2&3\\4&5\end{pmatrix}`,
		`\begin{pmatrix}1&2\end{aligned}`,
		`\begin{pmatrix}1&\\3&4\end{pmatrix}`,
		`\begin{pmatrix}&1\\3&4\end{pmatrix}`,
		`\begin{pmatrix}\end{pmatrix}`,
		`\begin{matrix}a&b\end{matrix}`,
		`\begin{pmatrix}1&2`,
	}
	for _, source := range sources {
		if got, ok := mathRender(source, true); ok {
			t.Fatalf("mathRender(%q, true) = %q, want rejected", source, got)
		}
	}
}

// Rust #57393: a spatial layout only renders when the whole grid fits the width
// minus the four-column nested prefix. Matrices report a required width even
// when they have a single row, so a narrow transcript falls back to the TeX
// source instead of truncating a bracket.
func TestMathSpatialLayoutsRequireWidthLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	singleRow := `\begin{pmatrix}10&20&30\end{pmatrix}`
	rendered, ok := mathRenderLayout(singleRow, mathModeDisplay)
	if !ok || !rendered.hasRequiredWidth {
		t.Fatalf("single-row matrix = %#v (ok=%v), want a required width", rendered, ok)
	}
	if rendered.requiredWidth <= 10-4 {
		t.Fatalf("single-row matrix required width = %d, want the wide grid", rendered.requiredWidth)
	}

	for _, source := range []string{
		`$$\begin{pmatrix}1&2\\3&4\end{pmatrix}$$`,
		`$$v=\begin{pmatrix}10&20&30\end{pmatrix}$$`,
		`$$\int_{0}^{\infty} e^{-x}\,dx = 1$$`,
	} {
		wide, _ := protectMathSpans(source, 80)
		if len(wide) != 1 {
			t.Fatalf("wide render of %q produced %#v, want one placeholder", source, wide)
		}
		if wide["8MATHPROT0END"] == source {
			t.Fatalf("wide render of %q restored the source instead of the layout", source)
		}
		narrow, _ := protectMathSpans(source, 10)
		if narrow["8MATHPROT0END"] != source {
			t.Fatalf("narrow render of %q produced %#v, want the verbatim source", source, narrow)
		}
	}
}

// Rust #57393: nested display math keeps the compact integral, and nested
// matrices stay verbatim because a list or blockquote cannot preserve their
// geometry. Mirrors the scanner half of the Rust snapshot.
func TestMathNestedDisplayIntegralAndMatrixLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	nestedIntegral := strings.Join(plainMarkdownLines(t, "- Integral:\n\n  $$\\int_0^1 x\\,dx$$", 80), "\n")
	if !strings.Contains(nestedIntegral, "∫₀¹ x dx") || strings.Contains(nestedIntegral, "\n") {
		t.Fatalf("nested display integral = %q, want the compact row", nestedIntegral)
	}
	topIntegral := strings.Join(plainMarkdownLines(t, "$$\\int_0^1 x\\,dx$$", 80), "\n")
	if strings.Count(topIntegral, "\n") != 2 || !strings.Contains(topIntegral, "∫ x dx") {
		t.Fatalf("top-level integral = %q, want stacked limits", topIntegral)
	}
	nestedMatrix := strings.Join(plainMarkdownLines(t, "- Matrix:\n\n  $$v=\\begin{pmatrix}10&20&30\\end{pmatrix}$$", 80), "\n")
	if !strings.Contains(nestedMatrix, `$$v=\begin{pmatrix}10&20&30\end{pmatrix}$$`) {
		t.Fatalf("nested matrix = %q, want the verbatim source", nestedMatrix)
	}
}
