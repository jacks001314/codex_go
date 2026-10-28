package utils

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateBytesAndTokens(t *testing.T) {
	content := "this is an example of a long output that should be truncated"
	bytes := FormattedTruncateText(content, BytesPolicy(30))
	if !strings.Contains(bytes, "Warning: truncated output") || !strings.Contains(bytes, "chars truncated") {
		t.Fatalf("byte truncation = %q", bytes)
	}
	if got := FormattedTruncateText("example output", TokensPolicy(10)); got != "example output" {
		t.Fatalf("under token limit = %q", got)
	}
	tokens := FormattedTruncateText(content, TokensPolicy(5))
	if !strings.Contains(tokens, "original token count: 15") || !strings.Contains(tokens, "tokens truncated") {
		t.Fatalf("token truncation = %q", tokens)
	}
}

func TestSplitStringRespectsUTF8Boundaries(t *testing.T) {
	removed, prefix, suffix := SplitString("😀abc😀", 5, 5)
	if removed != 1 || prefix != "😀a" || suffix != "c😀" {
		t.Fatalf("split = %d %q %q", removed, prefix, suffix)
	}
	removed, prefix, suffix = SplitString("abcdef", 4, 4)
	if removed != 0 || prefix != "abcd" || suffix != "ef" {
		t.Fatalf("overlap split = %d %q %q", removed, prefix, suffix)
	}
}

// Rust #57584 replaced the old text-merge helper with a unified formatter: for
// mixed output it budgets text in order and preserves opaque items without a
// truncation warning.
func TestFormattedMixedOutputPreservesEncryptedContent(t *testing.T) {
	encrypted := FunctionCallOutputContentItem{Kind: ContentEncryptedContent, EncryptedContent: "enc_opaque"}
	items := []FunctionCallOutputContentItem{
		{Kind: ContentInputText, Text: "abcdefgh"},
		encrypted,
	}
	output := FormattedTruncateFunctionOutputItemsWithPolicy(items, BytesPolicy(2), func(string) int { return 0 })
	if len(output) != 2 || output[0].Kind != ContentInputText || output[0].Text != "a\u20266 chars truncated\u2026h" {
		t.Fatalf("output items = %#v", output)
	}
	if output[1].Kind != ContentEncryptedContent || output[1].EncryptedContent != "enc_opaque" {
		t.Fatalf("encrypted item = %#v", output[1])
	}
}

func TestTruncateFunctionOutputItemsReportsOmittedText(t *testing.T) {
	chunk := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau\n"
	items := []FunctionCallOutputContentItem{
		{Kind: ContentInputText, Text: chunk},
		{Kind: ContentInputText, Text: strings.Repeat(chunk, 4)},
		{Kind: ContentInputImage, ImageURL: "img:mid"},
		{Kind: ContentInputText, Text: chunk},
	}
	output := TruncateFunctionOutputItemsWithPolicy(items, TokensPolicy(ApproxTokenCount(chunk)+1))
	if len(output) != 4 {
		t.Fatalf("output len = %d %#v", len(output), output)
	}
	if output[2].Kind != ContentInputImage {
		t.Fatalf("image not preserved: %#v", output)
	}
	if !strings.Contains(output[3].Text, "omitted 1 text items") {
		t.Fatalf("summary = %#v", output)
	}
}

// TestTruncateFunctionOutputItemsRetainsFileImages mirrors Rust #45794:
// truncation keeps image items verbatim, including uploaded-file references
// that the harness must not resolve.
func TestTruncateFunctionOutputItemsRetainsFileImages(t *testing.T) {
	chunk := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau\n"
	items := []FunctionCallOutputContentItem{
		{Kind: ContentInputText, Text: strings.Repeat(chunk, 4)},
		{Kind: ContentInputImage, FileID: "file_kept"},
	}
	output := TruncateFunctionOutputItemsWithPolicy(items, TokensPolicy(ApproxTokenCount(chunk)))
	var kept *FunctionCallOutputContentItem
	for i := range output {
		if output[i].Kind == ContentInputImage {
			kept = &output[i]
		}
	}
	if kept == nil || kept.FileID != "file_kept" || kept.ImageURL != "" {
		t.Fatalf("file image not retained: %#v", output)
	}
}

func TestApproxTokenConversions(t *testing.T) {
	if ApproxTokensFromByteCountInt64(-1) != 0 || ApproxTokensFromByteCountInt64(0) != 0 || ApproxTokensFromByteCountInt64(5) != 2 {
		t.Fatalf("unexpected int64 token conversion")
	}
}

// TestTruncateMiddleWithMarkerLikeRust mirrors Rust
// truncate_middle_with_marker (codex-rs/utils/string/src/truncate.rs).
func TestTruncateMiddleWithMarkerLikeRust(t *testing.T) {
	if got := TruncateMiddleWithMarker("short", 32, "|M|"); got != "short" {
		t.Fatalf("short = %q, want unchanged", got)
	}
	// A marker wider than the budget falls back to the longest prefix that fits.
	if got := TruncateMiddleWithMarker("abcdef", 3, "|||||"); got != "abc" {
		t.Fatalf("marker-too-wide = %q, want %q", got, "abc")
	}
	// 4 content bytes split 2/2 around the 3-byte marker.
	if got := TruncateMiddleWithMarker("0123456789", 7, "|M|"); got != "01|M|89" {
		t.Fatalf("middle = %q, want %q", got, "01|M|89")
	}
	// UTF-8 boundaries are respected and the marker sits outside the budget.
	got := TruncateMiddleWithMarker("é中😀é中😀", 9, "|M|")
	if len(got) > 9 || !utf8.ValidString(got) {
		t.Fatalf("utf8 result = %q (len %d)", got, len(got))
	}
}

// TestTruncateMiddleTokensKeepsPartialCharactersOutOfBothEnds mirrors Rust #49712:
// multibyte characters straddling a retained boundary are dropped whole, the
// retained prefix/suffix come from UTF-8 boundary lookups, and the original token
// estimate is still reported. The marker uses U+2026 like Rust's
// format_truncation_marker (codex-rs/utils/string/src/truncate.rs, #15572);
// the earlier Go-only ASCII "..." divergence is removed.
func TestTruncateMiddleTokensKeepsPartialCharactersOutOfBothEnds(t *testing.T) {
	for _, tc := range []struct {
		input    string
		expected string
	}{
		{input: "a😀b😀c", expected: "a…2 tokens truncated…c"},
		{input: "é中😀é", expected: "é…2 tokens truncated…é"},
		{input: "😀😀😀", expected: "…2 tokens truncated…"},
	} {
		truncated, tokens := TruncateMiddleWithTokenBudget(tc.input, 1)
		if truncated != tc.expected {
			t.Fatalf("TruncateMiddleWithTokenBudget(%q, 1) = %q, want %q", tc.input, truncated, tc.expected)
		}
		if tokens == nil || *tokens != 3 {
			t.Fatalf("original token count for %q = %v, want 3", tc.input, tokens)
		}
	}
}

// Rust `plan_token_truncation` (codex-rs/utils/string/src/truncate.rs:17) plans
// the head/tail edit without applying it: no plan when the text already fits, and
// otherwise the removed byte range plus the marker the truncation would use.
func TestPlanTokenTruncationMirrorsRustLikeRust(t *testing.T) {
	if plan := PlanTokenTruncation("short", 16); plan != nil {
		t.Fatalf("plan = %#v, want none inside the budget", plan)
	}
	text := strings.Repeat("word ", 200)
	plan := PlanTokenTruncation(text, 16)
	if plan == nil {
		t.Fatal("no plan for over-budget text")
	}
	if plan.RemovedStart != 32 || plan.RemovedEnd != 968 {
		t.Fatalf("removed = %d..%d, want 32..968", plan.RemovedStart, plan.RemovedEnd)
	}
	if plan.Marker != "\u2026234 tokens truncated\u2026" {
		t.Fatalf("marker = %q", plan.Marker)
	}
	// The applied string truncation is the plan's edit, and the byte variant keeps
	// its character-count marker.
	if got, want := text[:plan.RemovedStart]+plan.Marker+text[plan.RemovedEnd:], TruncateText(text, TokensPolicy(16)); got != want {
		t.Fatalf("plan = %q, truncate_text = %q", got, want)
	}
	if got, want := TruncateMiddleChars(text, 64), text[:32]+"\u2026936 chars truncated\u2026"+text[968:]; got != want {
		t.Fatalf("TruncateMiddleChars = %q, want %q", got, want)
	}
	if truncated, total := TruncateMiddleWithTokenBudget(text, 16); truncated != text[:32]+plan.Marker+text[968:] || total == nil || *total != 250 {
		t.Fatalf("TruncateMiddleWithTokenBudget = %q / %v", truncated, total)
	}
}

// Rust `truncate_middle_with_token_budget` (codex-rs/utils/string/src/truncate.rs:80-84)
// reports no truncation when the assembled text equals its input: the planned
// edit replaced exactly the bytes the notice re-inserts, so the second return
// value stays nil at that fixed point.
func TestTruncateMiddleWithTokenBudgetReportsTheFixedPointLikeRust(t *testing.T) {
	// max_tokens = 4 -> 16 bytes split 8/8; the 24-byte notice spans exactly the
	// removed range of this 40-byte input, so re-inserting it reproduces the input.
	text := "aaaaaaaa" + "\u20266 tokens truncated\u2026" + "bbbbbbbb"
	truncated, total := TruncateMiddleWithTokenBudget(text, 4)
	if truncated != text {
		t.Fatalf("truncated = %q, want the input unchanged", truncated)
	}
	if total != nil {
		t.Fatalf("total = %d, want nil at the fixed point", *total)
	}
	// One byte more does remove text, so the original token count is reported.
	over := text + "c"
	truncated, total = TruncateMiddleWithTokenBudget(over, 4)
	if truncated == over || total == nil || *total != uint64(ApproxTokenCount(over)) {
		t.Fatalf("over-budget = %q / %v, want a truncation with the original count", truncated, total)
	}
}
