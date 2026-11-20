package markdown

import (
	"strings"
	"testing"
)

// Rust parity: codex-rs/tui/src/markdown_render/math_display_tests.rs
// (unicode math snapshot, #62554). A single-grapheme \widetilde argument takes
// the same combining tilde as \tilde, in both the braced and unbraced forms.
func TestMathWidetildeUsesCombiningTildeLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	for _, argument := range []string{"{I}", " I"} {
		got := plainDisplayMath(t, `$$\frac{\widetilde`+argument+`}{2}$$`, 80)
		want := plainDisplayMath(t, `$$\frac{\tilde`+argument+`}{2}$$`, 80)
		if got != want {
			t.Fatalf("\\widetilde%s = %q, want the \\tilde render %q", argument, got, want)
		}
		if !strings.Contains(got, "I\u0303") {
			t.Fatalf("\\widetilde%s = %q, want a combining tilde over I", argument, got)
		}
	}

	// A multi-grapheme argument keeps the existing source fallback instead of
	// accenting only its first letter.
	if got := plainDisplayMath(t, `$$\frac{\widetilde{AB}}{2}$$`, 80); strings.Contains(got, "A\u0303") {
		t.Fatalf("\\widetilde{AB} = %q, want no accent on a multi-grapheme argument", got)
	}
}
