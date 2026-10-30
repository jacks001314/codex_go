package guardiancontext

import (
	"fmt"
	"unicode/utf8"

	"codex_go/utils"
)

// TruncateText truncates text using Guardian's approximate token budget and
// omission marker.
//
// It retains both ends on UTF-8 boundaries. Budgets smaller than the marker
// still return the marker, so callers should reserve room for that fixed
// overhead. Mirrors Rust's `codex_guardian_context::truncate_text`.
func TruncateText(text string, maxTokens int) string {
	maxBytes := utils.ApproxBytesForTokens(maxTokens)
	if len(text) <= maxBytes {
		return text
	}
	omittedTokens := utils.ApproxTokensFromByteCount(len(text) - maxBytes)
	marker := fmt.Sprintf("<truncated omitted_approx_tokens=\"%d\" />", omittedTokens)
	if maxBytes <= len(marker) {
		return marker
	}
	availableBytes := maxBytes - len(marker)
	prefixBytes := availableBytes / 2
	suffixBytes := availableBytes - prefixBytes
	prefixEnd := prefixBytes
	for prefixEnd > 0 && !utf8.RuneStart(text[prefixEnd]) {
		prefixEnd--
	}
	suffixStart := len(text) - suffixBytes
	for suffixStart < len(text) && !utf8.RuneStart(text[suffixStart]) {
		suffixStart++
	}
	return text[:prefixEnd] + marker + text[suffixStart:]
}
