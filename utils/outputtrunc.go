package utils

import (
	"fmt"
	"strings"
	"unicode/utf8"
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
	ContentEncryptedContent ContentItemKind = "encrypted_content"
)

type FunctionCallOutputContentItem struct {
	Kind             ContentItemKind
	Text             string
	ImageURL         string
	Detail           *string
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

func FormattedTruncateText(content string, policy TruncationPolicy) string {
	if len(content) <= (&policy).ByteBudget() {
		return content
	}
	originalTokenCount := ApproxTokenCount(content)
	totalLines := countLines(content)
	result := TruncateText(content, policy)
	return fmt.Sprintf("Warning: truncated output (original token count: %d)\nTotal output lines: %d\n\n%s", originalTokenCount, totalLines, result)
}

func TruncateText(content string, policy TruncationPolicy) string {
	if policy.Mode == PolicyTokens {
		out, _ := TruncateMiddleWithTokenBudget(content, policy.Limit)
		return out
	}
	return TruncateMiddleChars(content, policy.Limit)
}

func FormattedTruncateTextContentItemsWithPolicy(items []FunctionCallOutputContentItem, policy TruncationPolicy) ([]FunctionCallOutputContentItem, *int) {
	textSegments := make([]string, 0)
	for i := range items {
		item := &items[i]
		if item.Kind == ContentInputText {
			textSegments = append(textSegments, item.Text)
		}
	}
	if len(textSegments) == 0 {
		return cloneItems(items), nil
	}
	combined := strings.Join(textSegments, "\n")
	if len(combined) <= (&policy).ByteBudget() {
		return cloneItems(items), nil
	}
	originalTokenCount := ApproxTokenCount(combined)
	out := []FunctionCallOutputContentItem{{
		Kind: ContentInputText,
		Text: FormattedTruncateText(combined, policy),
	}}
	for i := range items {
		item := &items[i]
		switch item.Kind {
		case ContentInputImage, ContentEncryptedContent:
			out = append(out, *item)
		}
	}
	return out, &originalTokenCount
}

func TruncateFunctionOutputItemsWithPolicy(items []FunctionCallOutputContentItem, policy TruncationPolicy) []FunctionCallOutputContentItem {
	out := make([]FunctionCallOutputContentItem, 0, len(items))
	remainingBudget := (&policy).ByteBudget()
	if policy.Mode == PolicyTokens {
		remainingBudget = (&policy).TokenBudget()
	}
	omittedTextItems := 0

	for i := range items {
		item := &items[i]
		switch item.Kind {
		case ContentInputText:
			if remainingBudget == 0 {
				omittedTextItems++
				continue
			}
			cost := len(item.Text)
			if policy.Mode == PolicyTokens {
				cost = ApproxTokenCount(item.Text)
			}
			if cost <= remainingBudget {
				out = append(out, *item)
				remainingBudget -= cost
				continue
			}
			snippetPolicy := TruncationPolicy{Mode: policy.Mode, Limit: remainingBudget}
			snippet := TruncateText(item.Text, snippetPolicy)
			if snippet == "" {
				omittedTextItems++
			} else {
				truncated := *item
				truncated.Text = snippet
				out = append(out, truncated)
			}
			remainingBudget = 0
		case ContentInputImage, ContentEncryptedContent:
			out = append(out, *item)
		}
	}

	if omittedTextItems > 0 {
		out = append(out, FunctionCallOutputContentItem{
			Kind: ContentInputText,
			Text: fmt.Sprintf("[omitted %d text items ...]", omittedTextItems),
		})
	}
	return out
}

func TruncateMiddleChars(s string, maxBytes int) string {
	return truncateWithByteEstimate(s, maxBytes, false)
}

func TruncateMiddleWithTokenBudget(s string, maxTokens int) (string, *uint64) {
	if s == "" {
		return "", nil
	}
	if maxTokens > 0 && len(s) <= ApproxBytesForTokens(maxTokens) {
		return s, nil
	}
	truncated := truncateWithByteEstimate(s, ApproxBytesForTokens(maxTokens), true)
	totalTokens := uint64(ApproxTokenCount(s))
	if truncated == s {
		return truncated, nil
	}
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

func truncateWithByteEstimate(s string, maxBytes int, useTokens bool) string {
	if s == "" {
		return ""
	}
	totalChars := utf8.RuneCountInString(s)
	if maxBytes <= 0 {
		return formatTruncationMarker(useTokens, removedUnits(useTokens, len(s), totalChars))
	}
	if len(s) <= maxBytes {
		return s
	}
	leftBudget, rightBudget := splitBudget(maxBytes)
	removedChars, left, right := SplitString(s, leftBudget, rightBudget)
	marker := formatTruncationMarker(useTokens, removedUnits(useTokens, len(s)-maxBytes, removedChars))
	return left + marker + right
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
		return fmt.Sprintf("...%d tokens truncated...", removedCount)
	}
	return fmt.Sprintf("...%d chars truncated...", removedCount)
}

func removedUnits(useTokens bool, removedBytes int, removedChars int) uint64 {
	if useTokens {
		return ApproxTokensFromByteCount(removedBytes)
	}
	if removedChars <= 0 {
		return 0
	}
	return uint64(removedChars)
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
