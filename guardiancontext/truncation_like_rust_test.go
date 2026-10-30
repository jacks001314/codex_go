package guardiancontext

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"codex_go/utils"
)

// Mirrors truncation_preserves_prefix_suffix_and_utf8_boundaries.
func TestTruncationPreservesPrefixSuffixAndUTF8BoundariesLikeRust(t *testing.T) {
	text := "start " + strings.Repeat("é🙂", 2000) + " end"
	truncated := TruncateText(text, 200)
	maxBytes := utils.ApproxBytesForTokens(200)
	omittedTokens := utils.ApproxTokensFromByteCount(len(text) - maxBytes)
	marker := fmt.Sprintf("<truncated omitted_approx_tokens=\"%d\" />", omittedTokens)
	if !strings.HasPrefix(truncated, "start ") {
		t.Fatalf("truncated text lost its prefix: %q", truncated)
	}
	if !strings.HasSuffix(truncated, " end") {
		t.Fatalf("truncated text lost its suffix: %q", truncated)
	}
	if !strings.Contains(truncated, marker) {
		t.Fatalf("truncated text missing marker %q: %q", marker, truncated)
	}
	if len(truncated) > maxBytes {
		t.Fatalf("truncated bytes = %d, want <= %d", len(truncated), maxBytes)
	}
	if !utf8.ValidString(truncated) {
		t.Fatal("truncation split a UTF-8 sequence")
	}

	if got := TruncateText("é🙂", 2); got != "é🙂" {
		t.Fatalf("TruncateText within budget = %q", got)
	}
	if got := TruncateText("", 0); got != "" {
		t.Fatalf("TruncateText empty = %q", got)
	}
	if got, want := TruncateText("é🙂", 0), "<truncated omitted_approx_tokens=\"2\" />"; got != want {
		t.Fatalf("TruncateText zero budget = %q, want %q", got, want)
	}
	if got, want := TruncateText("é🙂", 1), "<truncated omitted_approx_tokens=\"1\" />"; got != want {
		t.Fatalf("TruncateText marker-only budget = %q, want %q", got, want)
	}
}
