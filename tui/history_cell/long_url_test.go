package historycell

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func TestUserPromptLongURLWrapsPreservingGutterAndBackground(t *testing.T) {
	longURL := "https://example.test/forwarded/threads/10930?page=1&queue=customer_support_unprocessed&forwardedScope=all"
	cell := NewUserPrompt(longURL, nil, nil, nil)
	lines := cell.DisplayLines(36)
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(utils.StripANSI(line)) == "" {
			continue
		}
		clean = append(clean, utils.StripANSI(line))
	}
	if len(clean) < 2 {
		t.Fatalf("long URL did not wrap: %#v", lines)
	}
	if !strings.HasPrefix(clean[0], "› ") {
		t.Fatalf("first user row should keep prompt: %q", clean[0])
	}
	for _, line := range clean[1:] {
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("continuation row should keep gutter: %q", line)
		}
	}
	for _, line := range lines {
		if !strings.Contains(line, ansiUserMessageBackground) {
			t.Fatalf("user row lost background: %q", line)
		}
	}
}

// Mirrors Rust #47360: URL destinations survive adaptive wrapping and forced
// splitting of oversized URLs at every terminal width.
func TestUserPromptURLsKeepDestinationsAcrossWidthsLikeRust(t *testing.T) {
	url := "https://example.test/terminal-link-wrapping/solid-octants"
	message := "from this site, write out the ascii for solid octants 18 columns " + url +
		"\n[Image #1] See (https://example.test/a)."
	cell := NewUserPrompt(message, nil, nil, nil)
	for _, width := range []int{120, 60, 24} {
		lines := cell.DisplayLines(width)
		joined := strings.Join(lines, "\n")
		for _, destination := range []string{url, "https://example.test/a"} {
			if !strings.Contains(joined, "\x1b]8;;"+destination+"\x07") {
				t.Fatalf("width %d lost URL destination %q:\n%q", width, destination, joined)
			}
		}
		for _, line := range lines {
			if got := codextui.DisplayWidth(utils.StripANSI(line)); got > width {
				t.Fatalf("width %d line = %d columns: %q", width, got, line)
			}
		}
	}
}
