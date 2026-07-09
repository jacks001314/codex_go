package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Rust parity: codex-rs/tui/src/mention_codec.rs.

const (
	ToolMentionSigil       rune = '$'
	PluginTextMentionSigil rune = '@'
)

type LinkedMention struct {
	Sigil   rune
	Mention string
	Path    string
}

type DecodedHistoryText struct {
	Text     string
	Mentions []LinkedMention
}

func EncodeMention(kind string, id string) string {
	return strings.TrimSpace(kind) + "://" + strings.TrimSpace(id)
}

func EncodeHistoryMentions(text string, mentions []LinkedMention) string {
	if text == "" || len(mentions) == 0 {
		return text
	}
	mentionsByToken := map[mentionToken][]string{}
	for _, mention := range mentions {
		if mention.Sigil != ToolMentionSigil && mention.Sigil != PluginTextMentionSigil {
			continue
		}
		name := strings.TrimSpace(mention.Mention)
		path := strings.TrimSpace(mention.Path)
		if name == "" || path == "" {
			continue
		}
		token := mentionToken{Sigil: mention.Sigil, Name: name}
		mentionsByToken[token] = append(mentionsByToken[token], path)
	}
	bytes := []byte(text)
	var out strings.Builder
	out.Grow(len(text))
	for index := 0; index < len(bytes); {
		if bytes[index] == byte(ToolMentionSigil) || bytes[index] == byte(PluginTextMentionSigil) {
			sigil := rune(bytes[index])
			if sigil == ToolMentionSigil || startsPlaintextMention(text, index) {
				nameStart := index + 1
				if nameStart < len(bytes) && isMentionNameByte(bytes[nameStart]) {
					nameEnd := nameStart + 1
					for nameEnd < len(bytes) && isMentionNameByte(bytes[nameEnd]) {
						nameEnd++
					}
					name := text[nameStart:nameEnd]
					if sigil == ToolMentionSigil || endsPlaintextMention(bytes, nameEnd) {
						token := mentionToken{Sigil: sigil, Name: name}
						queue := mentionsByToken[token]
						if len(queue) > 0 {
							path := queue[0]
							mentionsByToken[token] = queue[1:]
							out.WriteByte('[')
							out.WriteRune(sigil)
							out.WriteString(name)
							out.WriteString("](")
							out.WriteString(path)
							out.WriteByte(')')
							index = nameEnd
							continue
						}
					}
				}
			}
		}
		ch, size := nextRune(text[index:])
		out.WriteRune(ch)
		index += size
	}
	return out.String()
}

func DecodeHistoryMentions(text string) DecodedHistoryText {
	return DecodeHistoryMentionsWithAtMentions(text, true)
}

func DecodeHistoryMentionsWithAtMentions(text string, atMentionsEnabled bool) DecodedHistoryText {
	bytes := []byte(text)
	var out strings.Builder
	out.Grow(len(text))
	mentions := []LinkedMention{}
	for index := 0; index < len(bytes); {
		if bytes[index] == '[' {
			if sigil, name, path, end, ok := parseHistoryLinkedMention(text, bytes, index, atMentionsEnabled); ok {
				out.WriteRune(sigil)
				out.WriteString(name)
				mentions = append(mentions, LinkedMention{Sigil: sigil, Mention: name, Path: path})
				index = end
				continue
			}
		}
		ch, size := nextRune(text[index:])
		out.WriteRune(ch)
		index += size
	}
	return DecodedHistoryText{Text: out.String(), Mentions: mentions}
}

type mentionToken struct {
	Sigil rune
	Name  string
}

func parseHistoryLinkedMention(text string, textBytes []byte, start int, atMentionsEnabled bool) (rune, string, string, int, bool) {
	if name, path, end, ok := parseLinkedToolMention(text, textBytes, start, ToolMentionSigil); ok && !isCommonEnvVar(name) && isToolPath(path) {
		return ToolMentionSigil, name, path, end, true
	}
	if atMentionsEnabled {
		if name, path, end, ok := parseLinkedToolMention(text, textBytes, start, PluginTextMentionSigil); ok && !isCommonEnvVar(name) && isToolPath(path) {
			return PluginTextMentionSigil, name, path, end, true
		}
	} else if name, path, end, ok := parseLinkedToolMention(text, textBytes, start, PluginTextMentionSigil); ok && !isCommonEnvVar(name) && strings.HasPrefix(path, "plugin://") {
		return ToolMentionSigil, name, path, end, true
	}
	return 0, "", "", 0, false
}

func parseLinkedToolMention(text string, textBytes []byte, start int, sigil rune) (string, string, int, bool) {
	sigilIndex := start + 1
	if sigilIndex >= len(textBytes) || textBytes[sigilIndex] != byte(sigil) {
		return "", "", 0, false
	}
	nameStart := sigilIndex + 1
	if nameStart >= len(textBytes) || !isMentionNameByte(textBytes[nameStart]) {
		return "", "", 0, false
	}
	nameEnd := nameStart + 1
	for nameEnd < len(textBytes) && isMentionNameByte(textBytes[nameEnd]) {
		nameEnd++
	}
	if nameEnd >= len(textBytes) || textBytes[nameEnd] != ']' {
		return "", "", 0, false
	}
	pathStart := nameEnd + 1
	for pathStart < len(textBytes) && isASCIIWhitespace(textBytes[pathStart]) {
		pathStart++
	}
	if pathStart >= len(textBytes) || textBytes[pathStart] != '(' {
		return "", "", 0, false
	}
	pathEnd := pathStart + 1
	for pathEnd < len(textBytes) && textBytes[pathEnd] != ')' {
		pathEnd++
	}
	if pathEnd >= len(textBytes) || textBytes[pathEnd] != ')' {
		return "", "", 0, false
	}
	path := strings.TrimSpace(text[pathStart+1 : pathEnd])
	if path == "" {
		return "", "", 0, false
	}
	return text[nameStart:nameEnd], path, pathEnd + 1, true
}

func isMentionNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-'
}

func startsPlaintextMention(text string, index int) bool {
	if index == 0 {
		return true
	}
	prefix := text[:index]
	ch, _ := previousRune(prefix)
	return isUnicodeWhitespace(ch) || !isMentionNameRune(ch)
}

func endsPlaintextMention(textBytes []byte, index int) bool {
	if index >= len(textBytes) {
		return true
	}
	b := textBytes[index]
	if isASCIIWhitespace(b) {
		return true
	}
	if b == '.' {
		nextIndex := index + 1
		return nextIndex >= len(textBytes) || isASCIIWhitespace(textBytes[nextIndex]) || !isMentionNameByte(textBytes[nextIndex])
	}
	return b != '.' && b != '/' && b != '\\' && !isMentionNameByte(b)
}

func isMentionNameRune(ch rune) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-'
}

func isCommonEnvVar(name string) bool {
	switch strings.ToUpper(name) {
	case "PATH", "HOME", "USER", "SHELL", "PWD", "TMPDIR", "TEMP", "TMP", "LANG", "TERM", "XDG_CONFIG_HOME":
		return true
	default:
		return false
	}
}

func isToolPath(path string) bool {
	if strings.HasPrefix(path, "app://") || strings.HasPrefix(path, "mcp://") || strings.HasPrefix(path, "plugin://") || strings.HasPrefix(path, "skill://") {
		return true
	}
	name := path
	if idx := strings.LastIndexAny(path, `/\`); idx >= 0 {
		name = path[idx+1:]
	}
	return strings.EqualFold(name, "SKILL.md")
}

func isASCIIWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' || b == '\v'
}

func isUnicodeWhitespace(ch rune) bool {
	return unicode.IsSpace(ch)
}

func nextRune(text string) (rune, int) {
	ch, size := utf8.DecodeRuneInString(text)
	if ch == utf8.RuneError && size == 0 {
		return 0, 1
	}
	return ch, size
}

func previousRune(text string) (rune, int) {
	ch, size := utf8.DecodeLastRuneInString(text)
	if ch == utf8.RuneError && size == 0 {
		return 0, 0
	}
	return ch, size
}
