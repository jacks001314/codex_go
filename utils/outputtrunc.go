package utils

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"codex_go/audioutil"
)

const approxBytesPerToken = 4

type PolicyMode string

const (
	PolicyBytes  PolicyMode = "bytes"
	PolicyTokens PolicyMode = "tokens"
)

type TruncationPolicy struct {
	Mode  PolicyMode
	Limit int
}

type ContentItemKind string

const (
	ContentInputText        ContentItemKind = "input_text"
	ContentInputImage       ContentItemKind = "input_image"
	ContentInputAudio       ContentItemKind = "input_audio"
	ContentEncryptedContent ContentItemKind = "encrypted_content"
)

type FunctionCallOutputContentItem struct {
	Kind     ContentItemKind
	Text     string
	ImageURL string
	// FileID is the uploaded-file form of an image content item (Rust #45794):
	// truncation keeps these items intact instead of resolving them.
	FileID           string
	Detail           *string
	AudioURL         string
	EncryptedContent string
}

func BytesPolicy(limit int) TruncationPolicy {
	return TruncationPolicy{Mode: PolicyBytes, Limit: limit}
}

func TokensPolicy(limit int) TruncationPolicy {
	return TruncationPolicy{Mode: PolicyTokens, Limit: limit}
}

func (p *TruncationPolicy) ByteBudget() int {
	if p == nil {
		return 0
	}
	if p.Mode == PolicyTokens {
		return ApproxBytesForTokens(p.Limit)
	}
	if p.Limit < 0 {
		return 0
	}
	return p.Limit
}

func (p *TruncationPolicy) TokenBudget() int {
	if p == nil {
		return 0
	}
	if p.Mode == PolicyTokens {
		if p.Limit < 0 {
			return 0
		}
		return p.Limit
	}
	return int(ApproxTokensFromByteCount(p.ByteBudget()))
}

// WithSerializationAllowance mirrors Rust's
// `codex_utils_output_truncation::with_serialization_allowance`: the existing
// 20% allowance for serialization and headers. Saved history budgets already
// include this allowance, so callers must apply it only to the model's raw
// truncation policy. Rust multiplies the policy value in floating point and
// rounds up, preserving the policy's mode.
func (p TruncationPolicy) WithSerializationAllowance() TruncationPolicy {
	if p.Limit <= 0 {
		return p
	}
	return TruncationPolicy{Mode: p.Mode, Limit: int(math.Ceil(float64(p.Limit) * 1.2))}
}

func FormattedTruncateText(content string, policy TruncationPolicy) string {
	if len(content) <= (&policy).ByteBudget() {
		return content
	}
	return formatTruncatedText(content, policy, nil)
}

// formatTruncatedText prefixes the truncation warning and remaps boundary text
// offsets into the rendered snippet. Rust #57584
// `codex_utils_output_truncation::format_truncated_text`.
func formatTruncatedText(content string, policy TruncationPolicy, boundaries []OutputBoundary) string {
	originalTokenCount := ApproxTokenCount(content)
	totalLines := countLines(content)
	header := fmt.Sprintf("Warning: truncated output (original token count: %d)\nTotal output lines: %d\n\n", originalTokenCount, totalLines)
	snippet, edit := truncateTextWithPlan(content, policy)
	if edit != nil {
		for i := range boundaries {
			boundaries[i].TextOffset = renderedOffset(edit, boundaries[i].TextOffset, len(header))
		}
	}
	return header + snippet
}

func TruncateText(content string, policy TruncationPolicy) string {
	out, _ := truncateTextWithPlan(content, policy)
	return out
}

// truncateTextWithPlan returns the truncated text and, for token budgets, the
// edit that produced it so callers can remap annotated offsets. Only
// token-budget callers supply boundaries that need remapping, matching Rust
// #57584 `truncate_text_with_plan`.
func truncateTextWithPlan(content string, policy TruncationPolicy) (string, *TextTruncation) {
	if policy.Mode == PolicyTokens {
		edit := PlanTokenTruncation(content, policy.Limit)
		if edit == nil {
			return content, nil
		}
		return edit.Apply(content), edit
	}
	return TruncateMiddleChars(content, policy.Limit), nil
}

// OutputBoundary is an uncharged position in the original logical output.
// Token-boundary formatters update the item index and UTF-8 text offset to the
// rendered position, including when content at that position was omitted.
//
// At entry, item indices are clamped to the end of the input, text offsets
// round down to an in-bounds UTF-8 boundary, and offsets on non-text items are
// zeroed. Positions preceding an earlier boundary move to that position,
// preserving caller order. On return, boundaries refer to the rendered items;
// an end boundary precedes any generated omission notices. Rust #57584
// `OutputBoundary`.
type OutputBoundary struct {
	ItemIndex  int
	TextOffset int
}

// FormattedTruncateFunctionOutputItemsWithPolicy formats text-only output as a
// whole observation, including the truncation warning. Mixed text and audio
// output is charged against the budget in order while images, file references
// and encrypted content are preserved.
func FormattedTruncateFunctionOutputItemsWithPolicy(
	items []FunctionCallOutputContentItem,
	policy TruncationPolicy,
	estimateAudioTokenCount func(string) int,
) []FunctionCallOutputContentItem {
	return formattedOutput(items, policy, estimateAudioTokenCount, nil)
}

// FormattedOutputAtTokenBoundaries is FormattedTruncateFunctionOutputItemsWithPolicy
// with zero-width boundaries for callers that have already dropped output.
func FormattedOutputAtTokenBoundaries(
	items []FunctionCallOutputContentItem,
	maxTokens int,
	estimateAudioTokenCount func(string) int,
	boundaries []OutputBoundary,
) []FunctionCallOutputContentItem {
	normalizeBoundaries(items, boundaries)
	return formattedOutput(items, TokensPolicy(maxTokens), estimateAudioTokenCount, boundaries)
}

// MixedOutputAtTokenBoundaries budgets items in input order even if a caller
// previously dropped every media item.
func MixedOutputAtTokenBoundaries(
	items []FunctionCallOutputContentItem,
	maxTokens int,
	estimateAudioTokenCount func(string) int,
	boundaries []OutputBoundary,
) []FunctionCallOutputContentItem {
	normalizeBoundaries(items, boundaries)
	return truncateOutputItems(items, TokensPolicy(maxTokens), estimateAudioTokenCount, boundaries)
}

func formattedOutput(
	items []FunctionCallOutputContentItem,
	policy TruncationPolicy,
	estimateAudioTokenCount func(string) int,
	boundaries []OutputBoundary,
) []FunctionCallOutputContentItem {
	combinedLen := 0
	allText := true
	for i := range items {
		if items[i].Kind != ContentInputText {
			allText = false
			break
		}
		if combinedLen != 0 {
			combinedLen++
		}
		combinedLen += len(items[i].Text)
	}
	if !allText {
		return truncateOutputItems(items, policy, estimateAudioTokenCount, boundaries)
	}
	if combinedLen <= (&policy).ByteBudget() {
		return cloneItems(items)
	}

	var combined strings.Builder
	combined.Grow(combinedLen)
	nextBoundary := 0
	for index := range items {
		if combined.Len() != 0 {
			combined.WriteByte('\n')
		}
		for nextBoundary < len(boundaries) && boundaries[nextBoundary].ItemIndex == index {
			boundaries[nextBoundary].ItemIndex = 0
			boundaries[nextBoundary].TextOffset += combined.Len()
			nextBoundary++
		}
		combined.WriteString(items[index].Text)
	}
	// A boundary after the last item belongs at the end of the merged text.
	for i := nextBoundary; i < len(boundaries); i++ {
		boundaries[i].ItemIndex = 0
		boundaries[i].TextOffset = combined.Len()
	}
	return []FunctionCallOutputContentItem{{
		Kind: ContentInputText,
		Text: formatTruncatedText(combined.String(), policy, boundaries),
	}}
}

func normalizeBoundaries(items []FunctionCallOutputContentItem, boundaries []OutputBoundary) {
	previousIndex, previousOffset := 0, 0
	for i := range boundaries {
		itemIndex := boundaries[i].ItemIndex
		if itemIndex < 0 {
			itemIndex = 0
		}
		if itemIndex > len(items) {
			itemIndex = len(items)
		}
		textOffset := 0
		if itemIndex < len(items) && items[itemIndex].Kind == ContentInputText {
			offset := boundaries[i].TextOffset
			if offset < 0 {
				offset = 0
			}
			textOffset = floorCharBoundary(items[itemIndex].Text, offset)
		}
		if itemIndex > previousIndex || (itemIndex == previousIndex && textOffset > previousOffset) {
			previousIndex, previousOffset = itemIndex, textOffset
		}
		boundaries[i].ItemIndex = previousIndex
		boundaries[i].TextOffset = previousOffset
	}
}

// TruncateFunctionOutputItemsWithPolicy applies the byte/token budget without
// changing success metadata or media ordering.
func TruncateFunctionOutputItemsWithPolicy(items []FunctionCallOutputContentItem, policy TruncationPolicy) []FunctionCallOutputContentItem {
	return truncateOutputItems(items, policy, audioutil.EstimateAudioTokenCount, nil)
}

func truncateOutputItems(
	items []FunctionCallOutputContentItem,
	policy TruncationPolicy,
	estimateAudioTokenCount func(string) int,
	boundaries []OutputBoundary,
) []FunctionCallOutputContentItem {
	out := make([]FunctionCallOutputContentItem, 0, len(items))
	remainingBudget := (&policy).ByteBudget()
	if policy.Mode == PolicyTokens {
		remainingBudget = (&policy).TokenBudget()
	}
	omittedTextItems := 0
	omittedAudioItems := 0
	nextBoundary := 0

	for i := range items {
		item := &items[i]
		itemOut := len(out)
		itemPolicy := TruncationPolicy{Mode: policy.Mode, Limit: remainingBudget}
		var textEdit *TextTruncation
		keep := false
		switch item.Kind {
		case ContentInputText:
			switch {
			case item.Text == "":
				// Empty text contributes no model content but still consumes an
				// API array slot.
			case remainingBudget == 0:
				omittedTextItems++
			default:
				cost := len(item.Text)
				if policy.Mode == PolicyTokens {
					cost = ApproxTokenCount(item.Text)
				}
				if cost <= remainingBudget {
					remainingBudget -= cost
					keep = true
					break
				}
				var snippet string
				snippet, textEdit = truncateTextWithPlan(item.Text, itemPolicy)
				if snippet == "" {
					omittedTextItems++
				} else {
					truncated := *item
					truncated.Text = snippet
					out = append(out, truncated)
				}
				remainingBudget = 0
			}
		case ContentInputImage, ContentEncryptedContent:
			keep = true
		case ContentInputAudio:
			tokenCost := 0
			if estimateAudioTokenCount != nil {
				tokenCost = estimateAudioTokenCount(item.AudioURL)
			}
			cost := tokenCost
			if policy.Mode == PolicyBytes {
				cost = ApproxBytesForTokens(tokenCost)
			}
			if cost <= remainingBudget {
				remainingBudget -= cost
				keep = true
			} else {
				omittedAudioItems++
			}
		}
		for nextBoundary < len(boundaries) && boundaries[nextBoundary].ItemIndex == i {
			boundaries[nextBoundary].ItemIndex = itemOut
			if textEdit != nil {
				boundaries[nextBoundary].TextOffset = renderedOffset(textEdit, boundaries[nextBoundary].TextOffset, 0)
			} else if !keep {
				boundaries[nextBoundary].TextOffset = 0
			}
			nextBoundary++
		}
		if keep {
			out = append(out, *item)
		}
	}
	// End boundaries precede generated omission counts: counts are not original
	// content and must never move a collector notice away from its source.
	for i := nextBoundary; i < len(boundaries); i++ {
		boundaries[i].ItemIndex = len(out)
		boundaries[i].TextOffset = 0
	}

	if omittedTextItems > 0 {
		out = append(out, FunctionCallOutputContentItem{
			Kind: ContentInputText,
			Text: fmt.Sprintf("[omitted %d text items ...]", omittedTextItems),
		})
	}
	if omittedAudioItems > 0 {
		out = append(out, FunctionCallOutputContentItem{
			Kind: ContentInputText,
			Text: fmt.Sprintf("[omitted %d audio items ...]", omittedAudioItems),
		})
	}
	return out
}

func renderedOffset(edit *TextTruncation, boundary int, headerBytes int) int {
	if boundary == 0 {
		return 0
	}
	if boundary <= edit.RemovedStart {
		return headerBytes + boundary
	}
	removedAfterBoundary := 0
	if boundary > edit.RemovedEnd {
		removedAfterBoundary = boundary - edit.RemovedEnd
	}
	return headerBytes + edit.RemovedStart + len(edit.Marker) + removedAfterBoundary
}

// TextTruncation is the head/tail edit a token truncation performs: the byte
// range it removes and the marker that replaces it (Rust
// `codex_utils_string::truncate::TextTruncation`).
type TextTruncation struct {
	RemovedStart int
	RemovedEnd   int
	Marker       string
}

// Apply applies the planned edit to the string it was planned from (Rust
// `TextTruncation::apply`).
func (t *TextTruncation) Apply(original string) string {
	if t == nil {
		return original
	}
	start := t.RemovedStart
	if start < 0 {
		start = 0
	}
	if start > len(original) {
		start = len(original)
	}
	end := t.RemovedEnd
	if end < start {
		end = start
	}
	if end > len(original) {
		end = len(original)
	}
	return original[:start] + t.Marker + original[end:]
}

// PlanTokenTruncation plans the head/tail token truncation without applying it,
// so callers that keep content in parts can apply the same edit to each part and
// preserve the sources of the retained slices (Rust
// `codex_utils_string::truncate::plan_token_truncation`). The marker is outside
// the retained-text budget, exactly as in TruncateMiddleWithTokenBudget.
func PlanTokenTruncation(s string, maxTokens int) *TextTruncation {
	maxBytes := ApproxBytesForTokens(maxTokens)
	if len(s) <= maxBytes {
		return nil
	}
	leftBudget, rightBudget := splitBudget(maxBytes)
	return &TextTruncation{
		RemovedStart: floorCharBoundary(s, leftBudget),
		RemovedEnd:   ceilCharBoundary(s, len(s)-rightBudget),
		Marker:       formatTruncationMarker(true, ApproxTokensFromByteCount(len(s)-maxBytes)),
	}
}

// TruncateMiddleChars retains at most maxBytes from a string's ends and adds a
// character-count marker outside that budget (Rust `truncate_middle_chars`).
func TruncateMiddleChars(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	leftBudget, rightBudget := splitBudget(maxBytes)
	removedChars, left, right := SplitString(s, leftBudget, rightBudget)
	return left + formatTruncationMarker(false, uint64(removedChars)) + right
}

// TruncateMiddleWithMarker retains a UTF-8-safe prefix and suffix around marker
// so the complete result (including the marker) fits maxBytes. Mirrors Rust
// `truncate_middle_with_marker` (codex-rs/utils/string/src/truncate.rs): when
// the marker does not fit, the longest original prefix that does is returned.
func TruncateMiddleWithMarker(s string, maxBytes int, marker string) string {
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes < len(marker) {
		return s[:floorCharBoundary(s, maxBytes)]
	}
	leftBudget, rightBudget := splitBudget(maxBytes - len(marker))
	prefixEnd := floorCharBoundary(s, leftBudget)
	suffixStart := ceilCharBoundary(s, len(s)-rightBudget)
	return s[:prefixEnd] + marker + s[suffixStart:]
}

func TruncateMiddleWithTokenBudget(s string, maxTokens int) (string, *uint64) {
	truncation := PlanTokenTruncation(s, maxTokens)
	if truncation == nil {
		return s, nil
	}
	truncated := s[:truncation.RemovedStart] + truncation.Marker + s[truncation.RemovedEnd:]
	if truncated == s {
		// Rust `truncate_middle_with_token_budget`
		// (codex-rs/utils/string/src/truncate.rs:80-84) reports no truncation when the
		// assembled text equals its input: the planned edit replaced exactly the bytes
		// the notice re-inserts.
		return truncated, nil
	}
	totalTokens := uint64(ApproxTokenCount(s))
	return truncated, &totalTokens
}

func ApproxTokenCount(text string) int {
	return (len(text) + approxBytesPerToken - 1) / approxBytesPerToken
}

func ApproxBytesForTokens(tokens int) int {
	if tokens <= 0 {
		return 0
	}
	return tokens * approxBytesPerToken
}

func ApproxTokensFromByteCount(bytes int) uint64 {
	if bytes <= 0 {
		return 0
	}
	return uint64((bytes + approxBytesPerToken - 1) / approxBytesPerToken)
}

func ApproxTokensFromByteCountInt64(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	tokens := (bytes + approxBytesPerToken - 1) / approxBytesPerToken
	if tokens < 0 {
		return 0
	}
	return tokens
}

// floorCharBoundary returns the largest index at or below index that starts a
// UTF-8 sequence, clamped to the string bounds (Rust str::floor_char_boundary).
func floorCharBoundary(s string, index int) int {
	if index <= 0 {
		return 0
	}
	if index >= len(s) {
		return len(s)
	}
	for index > 0 && !utf8.RuneStart(s[index]) {
		index--
	}
	return index
}

// ceilCharBoundary returns the smallest index at or above index that starts a
// UTF-8 sequence, clamped to the string bounds (Rust str::ceil_char_boundary).
func ceilCharBoundary(s string, index int) int {
	if index <= 0 {
		return 0
	}
	if index >= len(s) {
		return len(s)
	}
	for index < len(s) && !utf8.RuneStart(s[index]) {
		index++
	}
	return index
}

func SplitString(s string, beginningBytes int, endBytes int) (int, string, string) {
	if s == "" {
		return 0, "", ""
	}
	if beginningBytes < 0 {
		beginningBytes = 0
	}
	if endBytes < 0 {
		endBytes = 0
	}
	length := len(s)
	tailStartTarget := length - endBytes
	if tailStartTarget < 0 {
		tailStartTarget = 0
	}
	prefixEnd := 0
	suffixStart := length
	removedChars := 0
	suffixStarted := false

	for idx, ch := range s {
		charEnd := idx + utf8.RuneLen(ch)
		if charEnd <= beginningBytes {
			prefixEnd = charEnd
			continue
		}
		if idx >= tailStartTarget {
			if !suffixStarted {
				suffixStart = idx
				suffixStarted = true
			}
			continue
		}
		removedChars++
	}
	if suffixStart < prefixEnd {
		suffixStart = prefixEnd
	}
	return removedChars, s[:prefixEnd], s[suffixStart:]
}

func splitBudget(budget int) (int, int) {
	if budget <= 0 {
		return 0, 0
	}
	left := budget / 2
	return left, budget - left
}

func formatTruncationMarker(useTokens bool, removedCount uint64) string {
	if useTokens {
		return fmt.Sprintf("\u2026%d tokens truncated\u2026", removedCount)
	}
	return fmt.Sprintf("\u2026%d chars truncated\u2026", removedCount)
}

func countLines(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}

func cloneItems(items []FunctionCallOutputContentItem) []FunctionCallOutputContentItem {
	out := make([]FunctionCallOutputContentItem, len(items))
	copy(out, items)
	return out
}
