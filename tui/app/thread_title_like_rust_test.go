package app

import (
	"strings"
	"testing"
)

// TestFallbackThreadTitleLikeRust covers Rust #60842 fallback_thread_title:
// the first message becomes a single line of plain text, capped at 60
// characters with an ellipsis.
func TestFallbackThreadTitleLikeRust(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Release triage for the API", "Release triage for the API"},
		{"whitespace", "  fix \n\n  spacing\there  ", "fix spacing here"},
		{"link", "See [the docs](https://example.com) first", "See the docs first"},
		{"emphasis and code", "**Bold** and `code` stay", "Bold and code stay"},
		{"heading and list", "# Title\n\n- one\n- two", "Title one two"},
		{"table text", "| a | b |\n| - | - |\n| 1 | 2 |", "| a | b | | - | - | | 1 | 2 |"},
		{"empty", "   \n\t ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FallbackThreadTitle(tc.in); got != tc.want {
				t.Fatalf("FallbackThreadTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFallbackThreadTitleBoundsLikeRust(t *testing.T) {
	if got := FallbackThreadTitle(strings.Repeat("ab ", 40)); got != strings.Repeat("ab ", 19)+"ab\u2026" {
		t.Fatalf("truncated title = %q", got)
	}
	// Exactly 60 characters stays as-is without an ellipsis.
	exact := strings.Repeat("x", 60)
	if got := FallbackThreadTitle(exact); got != exact {
		t.Fatalf("60-character title = %q", got)
	}
	// Unicode truncation counts characters, not bytes, and never splits one.
	got := FallbackThreadTitle(strings.Repeat("\u754c", 61))
	if want := strings.Repeat("\u754c", 59) + "\u2026"; got != want {
		t.Fatalf("unicode title = %q, want %q", got, want)
	}
	if n := len([]rune(got)); n != 60 {
		t.Fatalf("truncated runes = %d", n)
	}
	// Trailing spaces before the ellipsis are trimmed like Rust.
	spaced := FallbackThreadTitle(strings.Repeat("a", 58) + "   bbb")
	if want := strings.Repeat("a", 58) + "\u2026"; spaced != want {
		t.Fatalf("trimmed title = %q, want %q", spaced, want)
	}
}

// TestFallbackThreadTitleInputCapLikeRust keeps the markdown scan bounded to
// the first 2000 characters of the message.
func TestFallbackThreadTitleInputCapLikeRust(t *testing.T) {
	long := strings.Repeat("a", 2000) + " [tail](https://example.com)"
	if got := FallbackThreadTitle(long); got != strings.Repeat("a", 59)+"\u2026" {
		t.Fatalf("capped title = %q", got)
	}
}
