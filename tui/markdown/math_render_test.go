package markdown

import (
	"strings"
	"testing"
)

// Mirrors the inline (non-display) expectations of Rust's math tests: symbols,
// native/grouped scripts, accents, fractions and the structured commands.
func TestMathRenderInlineLikeRust(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{source: `\alpha^2 + \beta_{10}`, want: "α² + β₁₀"},
		{source: `\sqrt{x^2+y^2}`, want: "√(x²+y²)"},
		{source: `\frac{a}{b}`, want: "((a)/(b))"},
		{source: `\mathbb{R}^n`, want: "ℝⁿ"},
		{source: `x_i^2`, want: "xᵢ²"},
		{source: `\hat{x}`, want: "x\u0302"},
		{source: `\mathcal Q_t`, want: "𝒬ₜ"},
		{source: `\underset{x\ge0}{\min} x`, want: "(min)_{x≥0} x"},
		{source: `\left\langle\hat H\right\rangle`, want: "⟨H\u0302⟩"},
		{source: `\lfloor x\rfloor`, want: "⌊ x⌋"},
	}
	for _, testCase := range cases {
		got, ok := mathRender(testCase.source, false)
		if !ok || got != testCase.want {
			t.Errorf("mathRender(%q, false) = (%q, %v), want (%q, true)", testCase.source, got, ok, testCase.want)
		}
	}
}

// Mirrors Rust's rejections: ambiguous accent arguments and unsupported input
// stay source in both modes.
func TestMathRenderRejectionsLikeRust(t *testing.T) {
	sources := []string{
		`\hat{xy}`, `\bar{x+y}`, `\tilde{x^2}`, `\vec{\frac{x}{y}}`, `\dot{}`, `\ddot{ }`, `\hat`,
		`\unknown{x}`, `\frac{a}`, `{x`, `x}`, `^2`, `x^2^3`, `{a+b}^2`, `{x^2}^3`,
		`\frac{a}{b}^2`, `\sqrt[3]{x}`, `\sqrt [3]{x}`, `\left x`, `\left\alpha x\right\rangle`,
		`\left\ `, `\text{\alpha}`, `\begin{matrix}a&b\end{matrix}`,
		`\sum_i_j`, `\sum^i^j`, `\sum_`, `\sum^_i`,
		`\underset{x}`, `\substack{x&y}`,
	}
	for _, source := range sources {
		if got, ok := mathRender(source, false); ok {
			t.Errorf("mathRender(%q, false) = (%q, true), want rejected", source, got)
		}
		if got, ok := mathRender(source, true); ok {
			t.Errorf("mathRender(%q, true) = (%q, true), want rejected", source, got)
		}
	}
	// Nested fractions cannot preserve their hierarchy.
	for _, source := range []string{`\frac{\frac{a}{b}}{c}`, `\frac{a}{\frac{b}{c}}`} {
		if got, ok := mathRender(source, true); ok {
			t.Errorf("mathRender(%q, true) = (%q, true), want rejected", source, got)
		}
	}
}

// Mirrors Rust's display limits: the `_`/`^` order is immaterial and the stacked
// operator keeps its centered base.
func TestMathRenderDisplayLimitsLikeRust(t *testing.T) {
	first, okFirst := mathRender(`\sum_i^N x_i`, true)
	second, okSecond := mathRender(`\sum^N_i x_i`, true)
	if !okFirst || !okSecond || first != second {
		t.Fatalf("sum limit order changed the layout:\n%q (%v)\n%q (%v)", first, okFirst, second, okSecond)
	}
	if !strings.Contains(first, "N") || !strings.Contains(first, "∑") || !strings.Contains(first, "i") ||
		!strings.Contains(first, "xᵢ") || strings.Count(first, "\n") != 2 {
		t.Fatalf("stacked sum = %q", first)
	}
	// The big wedge (#48551) stacks its limits the same way.
	wedge, okWedge := mathRender(`\bigwedge_{j=0}^{n}`, true)
	if !okWedge || !strings.Contains(wedge, "⋀") || !strings.Contains(wedge, "j=0") || !strings.Contains(wedge, "n") {
		t.Fatalf("stacked bigwedge = %q (%v)", wedge, okWedge)
	}
	if strings.Count(wedge, "\n") != 2 {
		t.Fatalf("bigwedge rows = %q", wedge)
	}
}

// Mirrors Rust's structured bounds: aligned rows/cells, boxed and substack sizes
// stay bounded, and a 16-row aligned block lays out.
func TestMathRenderStructuredBoundsLikeRust(t *testing.T) {
	got, ok := mathRender(`\begin{aligned}`+strings.Repeat(`x&=1\\`, 16)+`\end{aligned}`, true)
	if !ok {
		t.Fatal("16-row aligned block was rejected")
	}
	if got != strings.Join([]string{"x =1", "x =1", "x =1", "x =1", "x =1", "x =1", "x =1", "x =1", "x =1", "x =1", "x =1", "x =1", "x =1", "x =1", "x =1", "x =1"}, "\n") {
		t.Fatalf("aligned rows = %q", got)
	}
	// A ninth row exceeds MAX_ROWS.
	if got, ok := mathRender(`\begin{aligned}`+strings.Repeat(`\sum_i x_i\\`, 9)+`\end{aligned}`, true); ok {
		t.Fatalf("oversized aligned block = %q, want rejected", got)
	}
	overLimit := []string{
		`\begin{aligned}` + strings.Repeat(`x&=1\\`, 17) + `\end{aligned}`,
		`\begin{aligned}` + strings.Repeat(`x&`, 17) + `x\end{aligned}`,
		`\boxed{` + strings.Repeat("x", 254) + `}`,
		strings.Repeat(`\boxed{`, 40) + "x" + strings.Repeat("}", 40),
		`\substack{` + strings.Repeat(`x\\`, 17) + `}`,
	}
	for _, source := range overLimit {
		if got, ok := mathRender(source, true); ok {
			t.Fatalf("over-limit structured input %q = %q, want rejected", source, got)
		}
	}
	invalid := []string{
		`\begin{aligned}x&=1`,
		`\begin{aligned}x&=1\end{matrix}`,
		`\begin{aligned}[b]x&=1\end{aligned}`,
		`\begin{aligned}x&={a&b}\end{aligned}`,
		`\begin{aligned}x&=1\\[bad]y&=2\end{aligned}`,
		`\begin{aligned}x&=1\\[NaNmm]y&=2\end{aligned}`,
	}
	for _, source := range invalid {
		if got, ok := mathRender(source, true); ok {
			t.Fatalf("invalid structured input %q = %q, want rejected", source, got)
		}
	}
}
