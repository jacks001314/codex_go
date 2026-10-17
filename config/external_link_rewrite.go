package config

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// rewriteExternalTermsPreservingLinks rewrites source-agent terminology outside
// recognized URLs and email addresses. Markdown labels and titles remain normal
// text, while destinations are copied verbatim. A source instruction filename
// that linkify would otherwise treat as a domain is still migrated, including
// relative paths and :line[:column] suffixes.
func rewriteExternalTermsPreservingLinks(content string, docFile string, rewrite func(string) string) string {
	var output strings.Builder
	start := 0
	for index := 0; index < len(content); {
		end, ok := externalLinkEnd(content, index, docFile)
		if !ok {
			_, size := utf8.DecodeRuneInString(content[index:])
			if size == 0 {
				size = 1
			}
			index += size
			continue
		}
		output.WriteString(rewrite(content[start:index]))
		output.WriteString(content[index:end])
		start = end
		index = end
	}
	output.WriteString(rewrite(content[start:]))
	return output.String()
}

func externalLinkEnd(content string, start int, docFile string) (int, bool) {
	if start >= len(content) || !externalLinkBoundary(content, start) {
		return 0, false
	}
	if end, ok := externalSchemeLinkEnd(content, start); ok {
		return end, true
	}
	end := externalLinkTokenEnd(content, start)
	if end <= start {
		return 0, false
	}
	token := content[start:end]
	if externalDocFileLinkToken(token, docFile) {
		return 0, false
	}
	if externalLinkLikeToken(token) {
		return end, true
	}
	return 0, false
}

func externalLinkBoundary(content string, start int) bool {
	if start == 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(content[:start])
	return !externalLinkNameRune(previous)
}

func externalLinkNameRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) ||
		value == '_' || value == '-' || value == '.' || value == '+' ||
		value == '%' || value == '@'
}

func externalSchemeLinkEnd(content string, start int) (int, bool) {
	index := start
	if index >= len(content) || !isASCIIAlpha(content[index]) {
		return 0, false
	}
	for index < len(content) && (isASCIIAlpha(content[index]) || isASCIIDigit(content[index]) ||
		content[index] == '+' || content[index] == '-' || content[index] == '.') {
		index++
	}
	if index+3 > len(content) || content[index:index+3] != "://" {
		return 0, false
	}
	return externalLinkTokenEnd(content, start), true
}

func externalLinkTokenEnd(content string, start int) int {
	index := start
	openParentheses := 0
	for index < len(content) {
		value, size := utf8.DecodeRuneInString(content[index:])
		if value == '(' {
			openParentheses++
		} else if value == ')' {
			if openParentheses == 0 {
				break
			}
			openParentheses--
		} else if externalLinkDelimiter(value) {
			break
		}
		index += size
	}
	return index
}

func externalLinkDelimiter(value rune) bool {
	if unicode.IsSpace(value) {
		return true
	}
	switch value {
	case '<', '>', '"', '`', '[', ']', '，', '。', '；', '：', '！', '？', '、', '（', '）', '【', '】', '《', '》', '“', '”', '‘', '’':
		return true
	default:
		return false
	}
}

func externalDocFileLinkToken(token string, docFile string) bool {
	if path, _, found := strings.Cut(token, "?"); found {
		token = path
	}
	if path, _, found := strings.Cut(token, "#"); found {
		token = path
	}
	parts := strings.SplitN(token, ":", 3)
	if !strings.EqualFold(parts[0], docFile) {
		return false
	}
	for _, part := range parts[1:] {
		if part == "" {
			return false
		}
		for _, value := range []byte(part) {
			if !isASCIIDigit(value) {
				return false
			}
		}
	}
	return true
}

func externalLinkLikeToken(token string) bool {
	if token == "" || strings.HasPrefix(token, ".") || strings.HasPrefix(token, "/") {
		return false
	}
	if strings.Contains(token, "@") {
		return true
	}
	lower := strings.ToLower(token)
	if strings.HasPrefix(lower, "www.") {
		return true
	}
	domain := token
	if slash := strings.IndexByte(domain, '/'); slash >= 0 {
		domain = domain[:slash]
	}
	dot := strings.LastIndexByte(domain, '.')
	if dot <= 0 || dot+1 >= len(domain) {
		return false
	}
	suffix := domain[dot+1:]
	if len(suffix) < 2 {
		return false
	}
	for index := 0; index < len(suffix); index++ {
		if !isASCIIAlpha(suffix[index]) {
			return false
		}
	}
	return true
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isASCIIDigit(value byte) bool {
	return value >= '0' && value <= '9'
}
