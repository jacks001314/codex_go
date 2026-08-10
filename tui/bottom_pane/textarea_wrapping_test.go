package bottompane

import (
	"strings"
	"testing"
)

func TestWrappedTextAreaWhitespaceStaysWithFollowingWord(t *testing.T) {
	// "aa bbb c" at width 4: the space after "aa" must not start a blank row;
	// it stays attached to "bbb" which wraps to the next row.
	lines := wrappedTextAreaLines("aa bbb c", 4)
	if len(lines) < 2 {
		t.Fatalf("lines = %+v, want at least 2", lines)
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" && len(lines) > 1 {
			t.Fatalf("whitespace-only row produced: %+v", lines)
		}
	}
}

func TestWrappedTextAreaUnicodeWhitespaceStaysWithFollowingWord(t *testing.T) {
	// Full-width space (U+3000) is breakable Unicode whitespace; it must stay
	// with the following word rather than occupying its own row.
	lines := wrappedTextAreaLines("aaa\u3000bbb", 4)
	if len(lines) < 2 {
		t.Fatalf("lines = %+v, want wrapped", lines)
	}
	if strings.TrimSpace(lines[0]) == "" {
		t.Fatalf("whitespace-only leading row: %+v", lines)
	}
}

func TestWrappedTextAreaNonbreakingWhitespaceStaysInWord(t *testing.T) {
	// Nonbreaking space (U+00A0) is part of its word: it never starts a
	// whitespace-only row ahead of following text.
	lines := wrappedTextAreaLines("aa\u00a0bb cc", 4)
	joined := strings.Join(lines, "|")
	if !strings.Contains(joined, "\u00a0") {
		t.Fatalf("nonbreaking space lost from word: %#v", lines)
	}
	for i, line := range lines {
		if i < len(lines)-1 && strings.TrimSpace(line) == "" {
			t.Fatalf("nonbreaking whitespace produced blank row: %#v", lines)
		}
	}
}

func TestWrappedTextAreaHyphenatedWordBreakpointPreserved(t *testing.T) {
	// The hyphen is a semantic breakpoint: the first row ends at the hyphen
	// and the remainder reflows (Rust wrapping.rs keeps breakpoints).
	lines := wrappedTextAreaLines("cross-platform", 6)
	if len(lines) < 2 || !strings.HasSuffix(lines[0], "-") {
		t.Fatalf("lines = %+v, want first row ending at hyphen", lines)
	}
}

func TestWrappedTextAreaFullLineReservesInsertionRow(t *testing.T) {
	lines := wrappedTextAreaLines("abcdefgh", 8)
	if len(lines) < 2 {
		t.Fatalf("lines = %+v, want continuation row for full line", lines)
	}
}

func TestWrappedTextAreaReflowsAcrossWrappedFragments(t *testing.T) {
	lines := wrappedTextAreaLines("aaa bbb ccc ddd", 6)
	joined := strings.Join(lines, "")
	if strings.Contains(joined, " ") && len(strings.Fields(joined)) != 4 {
		t.Fatalf("reflow lost words: %+v", lines)
	}
}

func TestWrappedTextAreaMaxLengthUnbrokenInputStaysLinear(t *testing.T) {
	long := strings.Repeat("x", 10_000)
	lines := wrappedTextAreaLines(long, 80)
	// 10000/80 = 125 full rows plus the insertion row reserved for the final
	// full-width logical line (Rust ad6e48ddd3).
	if len(lines) != 126 {
		t.Fatalf("lines = %d, want 126 (125 rows + insertion row)", len(lines))
	}
}
