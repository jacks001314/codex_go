package utils

import (
	"strings"
	"testing"
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

func TestFormattedContentItemsMergesTextAndPreservesOpaqueItems(t *testing.T) {
	detail := "high"
	items := []FunctionCallOutputContentItem{
		{Kind: ContentInputText, Text: "abcd"},
		{Kind: ContentInputImage, ImageURL: "img:one", Detail: &detail},
		{Kind: ContentInputText, Text: "efgh"},
		{Kind: ContentEncryptedContent, EncryptedContent: "enc_opaque"},
	}
	output, original := FormattedTruncateTextContentItemsWithPolicy(items, BytesPolicy(4))
	if original == nil || *original != 3 {
		t.Fatalf("original token count = %v", original)
	}
	if len(output) != 3 || output[0].Kind != ContentInputText || output[1].Kind != ContentInputImage || output[2].Kind != ContentEncryptedContent {
		t.Fatalf("output items = %#v", output)
	}
	if !strings.Contains(output[0].Text, "truncated output") {
		t.Fatalf("merged text = %q", output[0].Text)
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

func TestApproxTokenConversions(t *testing.T) {
	if ApproxTokensFromByteCountInt64(-1) != 0 || ApproxTokensFromByteCountInt64(0) != 0 || ApproxTokensFromByteCountInt64(5) != 2 {
		t.Fatalf("unexpected int64 token conversion")
	}
}
