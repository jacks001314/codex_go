package tui

import (
	"strings"
	"testing"
)

// Rust #46710: restored history filters untrusted control characters before it
// reaches the terminal, while keeping the styling and links history cells add.
func TestSanitizeHistoryTextDropsUntrustedControlsLikeRust(t *testing.T) {
	input := "\x1b[2J\x1b]52;c;Y2xpcA==\x07\u009dhidden\u009c\r\n\t https://example.com/path"
	got := SanitizeHistoryText(input)
	for _, forbidden := range []string{"\x1b[2J", "\x07", "\r", "\t", "\u009d"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized text kept %q: %q", forbidden, got)
		}
	}
	for _, want := range []string{"[2J]", "52;c;Y2xpcA==", "hidden", "https://example.com/path"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sanitized text dropped %q: %q", want, got)
		}
	}
}

func TestSanitizeHistoryTextKeepsStyleAndHyperlinks(t *testing.T) {
	styled := "\x1b[32m+1\x1b[0m"
	if got := SanitizeHistoryText(styled); got != styled {
		t.Fatalf("styled row = %q, want %q", got, styled)
	}
	link := "\x1b]8;;https://example.com/a\x07link\x1b]8;;\x07"
	if got := SanitizeHistoryText(link); got != link {
		t.Fatalf("hyperlink row = %q, want %q", got, link)
	}
}
