package markdown

import (
	"strings"
	"testing"
)

// Rust #46266 "Expand Unicode math rendering with accents, symbols, and
// delimiters": the symbol table grew to physics, relations, sets, logic, arrows
// and integral families, named delimiters work with and without \left/\right,
// and accents apply to single graphemes while keeping following scripts.
//
// The expected strings are the values in Rust's
// `unicode_math_accents_and_symbols` and `unicode_math_named_delimiters`
// snapshots.
func TestMathSymbolsAndAccentsLikeRust(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		// Operators and accents with following scripts.
		{source: `\hat H^2 + \hbar^2`, want: "H\u0302² + ℏ²"},
		{source: `\ell`, want: "ℓ"},
		{source: `A\dagger`, want: "A†"},
		{source: `B\ddagger`, want: "B‡"},
		{source: `\bar{x}_1`, want: "x\u0304₁"},
		{source: `\tilde{x}^2`, want: "x\u0303²"},
		{source: `\vec{v}_i`, want: "v\u20d7ᵢ"},
		{source: `\dot{x}`, want: "x\u0307"},
		{source: `\ddot{x}`, want: "x\u0308"},
		// Accents on Greek and script letters.
		{source: `\hat{\Psi}^2`, want: "Ψ\u0302²"},
		{source: `\bar{\varrho}`, want: "ϱ\u0304"},
		{source: `\tilde{\varsigma}`, want: "ς\u0303"},
		{source: `\hat{\imath}`, want: "ı\u0302"},
		// Greek variants and symbols.
		{source: `\varkappa+\varpi`, want: "ϰ+ϖ"},
		{source: `\Re z+\Im z`, want: "ℜ z+ℑ z"},
		{source: `\aleph_0`, want: "ℵ₀"},
		// Relations, geometry and operator symbols.
		{source: `a\propto b\sim c\simeq d\ll e\gg f`, want: "a∝ b∼ c≃ d≪ e≫ f"},
		{source: `a\perp b\parallel c`, want: "a⊥ b∥ c"},
		{source: `\angle ABC\cong\angle DEF`, want: "∠ ABC≅∠ DEF"},
		{source: `a\lesssim b\gtrsim c`, want: "a≲ b≳ c"},
		{source: `a\oplus b\otimes c\odot d`, want: "a⊕ b⊗ c⊙ d"},
		// Sets, logic, arrows and integrals.
		{source: `A\supseteq B\supset C\ni x`, want: "A⊇ B⊃ C∋ x"},
		{source: `A\setminus B=\varnothing`, want: "A∖ B=∅"},
		{source: `\neg P\land Q\lor R\implies S\iff T\impliedby U`, want: "¬ P∧ Q∨ R⇒ S⇔ T⇐ U"},
		{source: `\nexists x`, want: "∄ x"},
		{source: `a\leftrightarrow b\mapsto c\Leftarrow d`, want: "a↔ b↦ c⇐ d"},
		{source: `\uparrow\downarrow\updownarrow`, want: "↑↓↕"},
		{source: `\iint f`, want: "∬ f"},
		{source: `\iiint g`, want: "∭ g"},
		{source: `\oint h`, want: "∮ h"},
		{source: `\coprod A`, want: "∐ A"},
		{source: `\bigcup B`, want: "⋃ B"},
		{source: `\bigcap C`, want: "⋂ C"},
		// Punctuation symbols.
		{source: `x\prime`, want: "x′"},
		{source: `a\circ b\bullet c`, want: "a∘ b∙ c"},
		{source: `\vdots\ddots`, want: "⋮⋱"},
	}
	for _, testCase := range cases {
		got, ok := mathRender(testCase.source, false)
		if !ok || got != testCase.want {
			t.Errorf("mathRender(%q, false) = (%q, %v), want (%q, true)", testCase.source, got, ok, testCase.want)
		}
	}
}

// Named delimiters work both bare and through \left/\right, matching Rust's
// `unicode_math_named_delimiters` snapshot.
func TestMathNamedDelimitersLikeRust(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{source: `\left\langle\hat H\right\rangle`, want: "⟨H\u0302⟩"},
		{source: `\lfloor x\rfloor + \left\lceil y\right\rceil`, want: "⌊ x⌋ + ⌈ y⌉"},
		{source: `\left\lVert v\right\rVert`, want: "‖ v‖"},
		{source: `\lvert x\rvert`, want: "| x|"},
		{source: `\left\|w\right\|`, want: "‖w‖"},
		{source: `\left\{x\right\}`, want: "{x}"},
		{source: `\lbrace y\rbrace`, want: "{ y}"},
		{source: `\lbrack z\rbrack`, want: "[ z]"},
		{source: `\left<x\right>`, want: "⟨x⟩"},
	}
	for _, testCase := range cases {
		got, ok := mathRender(testCase.source, false)
		if !ok || got != testCase.want {
			t.Errorf("mathRender(%q, false) = (%q, %v), want (%q, true)", testCase.source, got, ok, testCase.want)
		}
	}
}

// An accent argument that is empty, invisible, or spans more than one grapheme
// keeps the source in both modes, matching Rust's
// `unicode_math_accents_reject_ambiguous_arguments`.
func TestMathAccentsRejectInvisibleAndMultiGraphemeArgumentsLikeRust(t *testing.T) {
	for _, source := range []string{`\hat{̂}`, `\bar{̂̂}`, `\vec{}`, "\\tilde{ }"} {
		if got, ok := mathRender(source, false); ok {
			t.Errorf("mathRender(%q, false) = (%q, true), want rejected", source, got)
		}
	}
}

// Rust's `unicode_math_schrodinger_equation_snapshot` renders the whole display
// equation; the Go layout produces the same rows.
func TestMathSchrodingerEquationLikeRust(t *testing.T) {
	// The delimiters themselves are stripped by the display-math extractor, so
	// the layout unit receives the formula body.
	source := "i\\hbar \\frac{\\partial}{\\partial t}\\Psi(\\mathbf r,t)\n=\n\\hat H\\Psi(\\mathbf r,t)\n=\n\\left[-\\frac{\\hbar^2}{2m}\\nabla^2+V(\\mathbf r,t)\\right]\\Psi(\\mathbf r,t)."
	got, ok := mathRender(source, true)
	if !ok {
		t.Fatalf("mathRender(schrodinger, true) was rejected")
	}
	// Rust's snapshot helper trims the right padding the layout adds to keep
	// rows rectangular.
	rows := strings.Split(got, "\n")
	for index := range rows {
		rows[index] = strings.TrimRight(rows[index], " ")
	}
	got = strings.Join(rows, "\n")
	want := "    ∂                       ℏ²\n" +
		"iℏ ───Ψ(r,t) = H\u0302Ψ(r,t) = [- ──∇²+V(r,t)]Ψ(r,t).\n" +
		"   ∂ t                      2m"
	if got != want {
		t.Fatalf("schrodinger layout =\n%s\nwant\n%s", got, want)
	}
}
