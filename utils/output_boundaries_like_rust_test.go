package utils

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

// Rust #57584 `formatted_text_output_reuses_owned_items_under_limit`.
func TestFormattedTextOutputReusesItemsUnderLimitLikeRust(t *testing.T) {
	items := []FunctionCallOutputContentItem{
		{Kind: ContentInputText, Text: "alpha"},
		{Kind: ContentInputText, Text: ""},
		{Kind: ContentInputText, Text: "beta"},
	}
	output := FormattedTruncateFunctionOutputItemsWithPolicy(items, BytesPolicy(32), func(string) int { return 0 })
	if len(output) != len(items) {
		t.Fatalf("output items = %#v", output)
	}
	for i := range items {
		if output[i] != items[i] {
			t.Fatalf("item %d = %#v, want %#v", i, output[i], items[i])
		}
	}
}

// Rust #57584 `formatted_text_output_preserves_empty_leading_text_behavior`.
func TestFormattedTextOutputPreservesEmptyLeadingTextLikeRust(t *testing.T) {
	items := []FunctionCallOutputContentItem{
		{Kind: ContentInputText, Text: ""},
		{Kind: ContentInputText, Text: "abc"},
	}
	output := FormattedTruncateFunctionOutputItemsWithPolicy(items, BytesPolicy(0), func(string) int { return 0 })
	want := "Warning: truncated output (original token count: 1)\nTotal output lines: 1\n\n\u20263 chars truncated\u2026"
	if len(output) != 1 || output[0].Kind != ContentInputText || output[0].Text != want {
		t.Fatalf("output = %#v", output)
	}
}

// Rust #57584 `formatted_text_output_merges_all_text_for_token_budget`.
func TestFormattedTextOutputMergesAllTextForTokenBudgetLikeRust(t *testing.T) {
	items := []FunctionCallOutputContentItem{
		{Kind: ContentInputText, Text: "abcdefgh"},
		{Kind: ContentInputText, Text: "ijklmnop"},
	}
	output := FormattedTruncateFunctionOutputItemsWithPolicy(items, TokensPolicy(2), func(string) int { return 0 })
	want := "Warning: truncated output (original token count: 5)\nTotal output lines: 2\n\nabcd\u20263 tokens truncated\u2026mnop"
	if len(output) != 1 || output[0].Kind != ContentInputText || output[0].Text != want {
		t.Fatalf("output = %#v", output)
	}
}

// Rust #57584 `formatted_mixed_output_budgets_text_and_audio_in_order`.
func TestFormattedMixedOutputBudgetsTextAndAudioInOrderLikeRust(t *testing.T) {
	audio := FunctionCallOutputContentItem{Kind: ContentInputAudio, AudioURL: "audio:one"}
	fileImage := FunctionCallOutputContentItem{Kind: ContentInputImage, FileID: "file_123"}
	items := []FunctionCallOutputContentItem{
		{Kind: ContentInputText, Text: "abcd"},
		{Kind: ContentInputImage, ImageURL: "img:one"},
		{Kind: ContentInputText, Text: "efgh"},
		audio,
		fileImage,
		{Kind: ContentInputText, Text: "ijkl"},
		{Kind: ContentInputImage, ImageURL: "img:two"},
	}
	output := FormattedTruncateFunctionOutputItemsWithPolicy(items, BytesPolicy(12), func(string) int { return 1 })
	wantKinds := []ContentItemKind{ContentInputText, ContentInputImage, ContentInputText, ContentInputAudio, ContentInputImage, ContentInputImage, ContentInputText}
	if len(output) != len(wantKinds) {
		t.Fatalf("output items = %#v", output)
	}
	for i, kind := range wantKinds {
		if output[i].Kind != kind {
			t.Fatalf("item %d kind = %q, want %q (%#v)", i, output[i].Kind, kind, output)
		}
	}
	if output[6].Text != "[omitted 1 text items ...]" {
		t.Fatalf("omission notice = %q", output[6].Text)
	}
	if output[3].AudioURL != "audio:one" || output[4].FileID != "file_123" {
		t.Fatalf("media not preserved: %#v", output)
	}
}

// Rust #57584 `truncate_function_output_items_with_policy_omits_audio_over_budget`.
func TestTruncateFunctionOutputItemsOmitsAudioOverBudgetLikeRust(t *testing.T) {
	items := []FunctionCallOutputContentItem{
		{Kind: ContentInputText, Text: "abcdefgh"},
		{Kind: ContentEncryptedContent, EncryptedContent: "enc_opaque"},
		{Kind: ContentInputAudio, AudioURL: "audio:one"},
	}
	output := truncateOutputItems(items, BytesPolicy(2), func(string) int { return 1 }, nil)
	if len(output) != 3 || output[0].Text != "a\u20266 chars truncated\u2026h" {
		t.Fatalf("output = %#v", output)
	}
	if output[1].Kind != ContentEncryptedContent || output[2].Text != "[omitted 1 audio items ...]" {
		t.Fatalf("output = %#v", output)
	}
}

func boundaryPairs(boundaries []OutputBoundary) [][2]int {
	out := make([][2]int, len(boundaries))
	for i, boundary := range boundaries {
		out[i] = [2]int{boundary.ItemIndex, boundary.TextOffset}
	}
	return out
}

func equalBoundaryPairs(got [][2]int, want [][2]int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func sortedBoundaryPairs(pairs [][2]int) bool {
	for i := 1; i < len(pairs); i++ {
		if pairs[i][0] < pairs[i-1][0] {
			return false
		}
		if pairs[i][0] == pairs[i-1][0] && pairs[i][1] < pairs[i-1][1] {
			return false
		}
	}
	return true
}

// Rust #57584 `boundary_formatters_normalize_input_and_remap_truncated_text`.
func TestBoundaryFormattersNormalizeInputAndRemapTruncatedTextLikeRust(t *testing.T) {
	audioCost := func(string) int { return 1 }
	items := []FunctionCallOutputContentItem{
		{Kind: ContentInputText, Text: "left-head"},
		{Kind: ContentInputText, Text: "right-tail"},
	}
	boundaries := []OutputBoundary{{ItemIndex: 0, TextOffset: 0}, {ItemIndex: 0, TextOffset: 2}, {ItemIndex: 1, TextOffset: 0}, {ItemIndex: 1, TextOffset: 8}, {ItemIndex: 2, TextOffset: 0}}
	output := FormattedOutputAtTokenBoundaries(items, 2, audioCost, boundaries)
	if len(output) != 1 || output[0].Kind != ContentInputText {
		t.Fatalf("output = %#v", output)
	}
	rendered := output[0].Text
	head := strings.Index(rendered, "left")
	if head < 0 || !strings.HasSuffix(rendered, "tail") {
		t.Fatalf("rendered = %q", rendered)
	}
	tail := len(rendered) - len("tail")
	want := [][2]int{{0, 0}, {0, head + 2}, {0, tail}, {0, tail + 2}, {0, len(rendered)}}
	if got := boundaryPairs(boundaries); !equalBoundaryPairs(got, want) {
		t.Fatalf("boundaries = %v, want %v (rendered %q)", got, want, rendered)
	}

	type middleCase struct {
		item      FunctionCallOutputContentItem
		middleEnd int
	}
	for _, middle := range []middleCase{
		{item: FunctionCallOutputContentItem{Kind: ContentInputText, Text: "after"}, middleEnd: 5},
		{item: FunctionCallOutputContentItem{Kind: ContentInputImage, ImageURL: "img:one"}, middleEnd: 0},
	} {
		items := []FunctionCallOutputContentItem{
			{Kind: ContentInputText, Text: "\U0001F642\u00e9 boundary"},
			middle.item,
			{Kind: ContentInputText, Text: "last"},
		}
		formatters := []func([]FunctionCallOutputContentItem, int, func(string) int, []OutputBoundary) []FunctionCallOutputContentItem{
			FormattedOutputAtTokenBoundaries,
			MixedOutputAtTokenBoundaries,
		}
		for _, formatter := range formatters {
			for _, tokens := range []int{0, 1, 2, 256} {
				bounded := []OutputBoundary{
					{ItemIndex: 0, TextOffset: 1},
					{ItemIndex: 1, TextOffset: math.MaxInt},
					{ItemIndex: 0, TextOffset: 8},
					{ItemIndex: math.MaxInt, TextOffset: math.MaxInt},
				}
				got := formatter(items, tokens, audioCost, bounded)
				plain := formatter(items, tokens, audioCost, nil)
				if len(got) != len(plain) {
					t.Fatalf("tokens %d: bounded output = %#v, plain = %#v", tokens, got, plain)
				}
				pairs := boundaryPairs(bounded)
				if !sortedBoundaryPairs(pairs) {
					t.Fatalf("tokens %d: boundaries not sorted: %v", tokens, pairs)
				}
				for _, pair := range pairs {
					index, offset := pair[0], pair[1]
					if index < len(got) && got[index].Kind == ContentInputText {
						if !utf8.ValidString(got[index].Text) || !isRuneStartAt(got[index].Text, offset) {
							t.Fatalf("tokens %d: offset %d is not a text boundary: %v", tokens, offset, pairs)
						}
						continue
					}
					if index > len(got) || offset != 0 {
						t.Fatalf("tokens %d: media/end boundary %v invalid for %d items", tokens, pair, len(got))
					}
				}
				if tokens == 256 {
					want := [][2]int{{0, 0}, {1, middle.middleEnd}, {1, middle.middleEnd}, {3, 0}}
					if !equalBoundaryPairs(pairs, want) {
						t.Fatalf("tokens 256: boundaries = %v, want %v", pairs, want)
					}
				}
			}
		}
	}
}

func isRuneStartAt(text string, offset int) bool {
	if offset < 0 || offset > len(text) {
		return false
	}
	return offset == len(text) || utf8.RuneStart(text[offset])
}

// Rust #57584 `owned_mixed_output_moves_a_preserved_image_across_text_truncation`.
func TestMixedOutputPreservesImageAcrossTextTruncationLikeRust(t *testing.T) {
	items := []FunctionCallOutputContentItem{
		{Kind: ContentInputText, Text: "long prefix"},
		{Kind: ContentInputImage, ImageURL: "data:image/png;base64," + strings.Repeat("a", 4096)},
		{Kind: ContentInputText, Text: "omitted"},
	}
	bounded := []OutputBoundary{{ItemIndex: 0, TextOffset: 5}, {ItemIndex: 2, TextOffset: 3}}
	actual := FormattedOutputAtTokenBoundaries(items, 1, func(string) int { return 0 }, bounded)
	expected := FormattedOutputAtTokenBoundaries(items, 1, func(string) int { return 0 }, nil)
	if len(actual) != len(expected) {
		t.Fatalf("actual = %#v, expected = %#v", actual, expected)
	}
	projected := boundaryPairs(bounded)
	want := [][2]int{{0, 0}, {2, 0}}
	if len(projected) != 2 || projected[1] != want[1] || projected[0][0] != 0 {
		t.Fatalf("projected boundaries = %v, want %v", projected, want)
	}
	index, offset := projected[0][0], projected[0][1]
	if index >= len(actual) || actual[index].Kind != ContentInputText {
		t.Fatalf("expected a boundary inside truncated text: %#v", actual)
	}
	if tail := actual[index].Text[offset:]; tail != "ix" {
		t.Fatalf("snippet tail = %q, want %q", tail, "ix")
	}
	if actual[1].Kind != ContentInputImage || !strings.HasPrefix(actual[1].ImageURL, "data:image/png;base64,") {
		t.Fatalf("expected preserved image: %#v", actual)
	}
}
