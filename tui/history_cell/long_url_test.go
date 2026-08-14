package historycell

import (
	"strings"
	"testing"

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
