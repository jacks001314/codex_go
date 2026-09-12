package tui

import (
	"strings"
	"unicode/utf8"
)

// Rust parity: codex-rs/tui/src/assistant_directives.rs. Parse structured
// annotations embedded in assistant-authored Markdown, e.g.
// `::git-create-pr{cwd="/repo" isDraft=true}` becomes the name `git-create-pr`
// and the attributes `cwd` and `isDraft`. Consumers choose the quote-escaping
// rules and interpret the attributes; the exact directive source is retained,
// excluding any trailing Markdown. Scanners can share a byte-work budget across
// unsuccessful parse attempts.

// AssistantDirective is an assistant annotation and its complete, unmodified
// source representation.
type AssistantDirective struct {
	Name       string
	Attributes map[string]string
	Raw        string
}

// QuoteEscaping selects the quote handling mode: git receipts preserve
// backslashes, review comments allow escaped quote delimiters.
type QuoteEscaping int

const (
	// QuoteEscapingLiteral preserves backslashes inside quoted values.
	QuoteEscapingLiteral QuoteEscaping = iota
	// QuoteEscapingBackslash treats `\"` (or `\'`) as a literal quote delimiter.
	QuoteEscapingBackslash
)

// ParseAssistantDirective parses one inline, leaf, or container marker from the
// beginning of source.
func ParseAssistantDirective(source string, escaping QuoteEscaping) (AssistantDirective, bool) {
	remaining := int(^uint(0) >> 1)
	return ParseAssistantDirectiveWithBudget(source, escaping, &remaining)
}

// ParseAssistantDirectiveWithBudget parses with a shared byte-scanning budget
// for callers that retry at multiple offsets.
//
// The budget is charged for inspected source, not the whole supplied suffix. A
// scan may finish its current token before exhausting the budget; subsequent
// attempts return immediately. This bounds repeated malformed candidates
// without imposing a fixed size or count limit on valid directives.
func ParseAssistantDirectiveWithBudget(source string, escaping QuoteEscaping, remaining *int) (AssistantDirective, bool) {
	if remaining == nil || !spendDirectiveScanBudget(remaining, 1) {
		return AssistantDirective{}, false
	}
	// `::git-create-pr{...}` starts with a one-to-three-colon marker and a name;
	// require `{` immediately after the name so `::git-create-pr prose` is not
	// parsed.
	rest := strings.TrimLeft(source, ":")
	colonCount := len(source) - len(rest)
	if !spendDirectiveScanBudget(remaining, colonCount) {
		return AssistantDirective{}, false
	}
	if colonCount < 1 || colonCount > 3 {
		return AssistantDirective{}, false
	}
	nameLen := 0
	for nameLen < len(rest) && isDirectiveNameByte(rest[nameLen]) {
		nameLen++
	}
	if !spendDirectiveScanBudget(remaining, nameLen+1) {
		return AssistantDirective{}, false
	}
	name := rest[:nameLen]
	suffix := rest[nameLen:]
	if !strings.HasPrefix(suffix, "{") {
		return AssistantDirective{}, false
	}
	rest = suffix[1:]
	if nameLen == 0 || !isASCIIAlpha(rune(name[0])) {
		return AssistantDirective{}, false
	}

	attributes := map[string]string{}
	for {
		var ok bool
		rest, ok = trimDirectiveAttributeSpace(rest, remaining)
		if !ok {
			return AssistantDirective{}, false
		}
		if strings.HasPrefix(rest, "}") {
			// In `::git-push{cwd="/repo"} done`, retain the directive but not ` done`.
			return AssistantDirective{
				Name:       name,
				Attributes: attributes,
				Raw:        source[:len(source)-(len(rest)-1)],
			}, true
		}
		// For `cwd = "/repo" isDraft=true}`, split off `cwd` and leave the next
		// attribute for the next iteration after consuming this value.
		keyLen := 0
		for keyLen < len(rest) && isDirectiveNameByte(rest[keyLen]) {
			keyLen++
		}
		if !spendDirectiveScanBudget(remaining, keyLen+1) {
			return AssistantDirective{}, false
		}
		key := rest[:keyLen]
		// Reject malformed keys and duplicates before scanning a potentially long
		// value.
		if key == "" {
			return AssistantDirective{}, false
		}
		if _, duplicate := attributes[key]; duplicate {
			return AssistantDirective{}, false
		}
		value, ok := trimDirectiveAttributeSpace(rest[keyLen:], remaining)
		if !ok || !strings.HasPrefix(value, "=") {
			return AssistantDirective{}, false
		}
		rest, ok = trimDirectiveAttributeSpace(value[1:], remaining)
		if !ok {
			return AssistantDirective{}, false
		}
		parsed, next, ok := scanDirectiveValue(rest, escaping, remaining)
		if !ok {
			return AssistantDirective{}, false
		}
		attributes[key] = parsed
		rest = next
	}
}

// scanDirectiveValue parses one quoted or unquoted attribute value and returns
// the decoded value plus the unconsumed suffix.
func scanDirectiveValue(rest string, escaping QuoteEscaping, remaining *int) (string, string, bool) {
	if len(rest) > 0 && (rest[0] == '"' || rest[0] == '\'') {
		// In `body="Keep \"x}\" literal."`, only the matching unescaped quote ends
		// the value, not the embedded `}`. Single-quoted values work too.
		delimiter := rest[0]
		quoted := rest[1:]
		end := -1
		index := 0
	scan:
		for index < len(quoted) {
			character, size := utf8.DecodeRuneInString(quoted[index:])
			if !spendDirectiveScanBudget(remaining, size) {
				return "", "", false
			}
			switch {
			case size == 1 && character == rune(delimiter):
				end = index
				break scan
			case character == '\n' || character == '\r':
				return "", "", false
			case character == '\\' && escaping == QuoteEscapingBackslash &&
				index+size < len(quoted) && quoted[index+size] == delimiter:
				// Backslash mode consumes `\"` as a literal quote. Literal mode
				// instead lets the quote close `cwd="/repo\"`.
				index += size
				if !spendDirectiveScanBudget(remaining, 1) {
					return "", "", false
				}
			}
			index += size
		}
		if end < 0 {
			return "", "", false
		}
		value := quoted[:end]
		if escaping == QuoteEscapingBackslash {
			escaped := "\\" + string(delimiter)
			value = strings.ReplaceAll(value, escaped, string(delimiter))
		}
		return value, quoted[end+1:], true
	}
	// Unquoted `isDraft=true` ends at whitespace or `}`, not at a quote.
	end := strings.IndexAny(rest, " \t}\n\r")
	if end < 0 {
		end = len(rest)
	}
	if !spendDirectiveScanBudget(remaining, end+1) {
		return "", "", false
	}
	if end == 0 {
		return "", "", false
	}
	return rest[:end], rest[end:], true
}

func trimDirectiveAttributeSpace(source string, remaining *int) (string, bool) {
	rest := strings.TrimLeft(source, " \t")
	if !spendDirectiveScanBudget(remaining, len(source)-len(rest)+1) {
		return "", false
	}
	return rest, true
}

func spendDirectiveScanBudget(remaining *int, scanned int) bool {
	available := *remaining
	*remaining -= scanned
	if *remaining < 0 {
		*remaining = 0
	}
	return scanned <= available
}

func isDirectiveNameByte(character byte) bool {
	return isASCIIAlphaNum(rune(character)) || character == '_' || character == '-'
}
