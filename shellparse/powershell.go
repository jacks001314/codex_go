package shellparse

import (
	"strings"
	"unicode"
)

// ParsePlainPowerShellCommands mirrors Rust
// codex_shell_command::powershell::parse_powershell_script_into_plain_commands,
// which lowers a deliberately small literal PowerShell subset into argv-like
// commands. Rust parses that subset with tree-sitter and rejects every shape it
// does not explicitly recognize; this port implements the same accepted subset
// with a hand-written scanner and fails closed for everything else.
//
// Accepted: literal words separated by whitespace, commands joined by `;`,
// newlines, `|`, `||`, `&&`, single-quoted strings (with doubled-quote escapes),
// double-quoted strings without expansion, backtick escapes other than the
// PowerShell-version-dependent ones, line and block comments, and
// `--flag=value` parameters. Dynamic syntax (`$`, `@`, sub-expressions,
// redirection, invocation operators, attached `-Parameter:value` forms) is
// rejected so callers fall back to the raw argv instead of judging an
// incomplete view of the command.
func ParsePlainPowerShellCommands(script string) ([][]string, bool) {
	// PowerShell treats these Unicode characters as syntax aliases even though a
	// parser may leave them inside generic tokens.
	for _, ch := range script {
		switch ch {
		case '\u2018', '\u2019', '\u201c', '\u201d', '\u2013', '\u2014', '\u2015':
			return nil, false
		}
	}
	parser := powerShellCommandParser{chars: []rune(script)}
	if !parser.parse() || len(parser.commands) == 0 {
		return nil, false
	}
	return parser.commands, true
}

type powerShellCommandParser struct {
	chars        []rune
	index        int
	commands     [][]string
	canChain     bool
	needsCommand bool
}

func (p *powerShellCommandParser) parse() bool {
	for p.index < len(p.chars) {
		ch := p.chars[p.index]
		switch {
		case ch == '\r' || ch == '\n':
			p.canChain = false
			p.index++
		case unicode.IsSpace(ch):
			p.index++
		case ch == ';':
			if p.needsCommand {
				return false
			}
			p.canChain = false
			p.index++
		case ch == '|':
			if !p.canChain {
				return false
			}
			p.canChain = false
			p.needsCommand = true
			p.index++
			if p.index < len(p.chars) && p.chars[p.index] == '|' {
				p.index++
			}
		case ch == '&':
			if !p.canChain || p.index+1 >= len(p.chars) || p.chars[p.index+1] != '&' {
				return false
			}
			p.canChain = false
			p.needsCommand = true
			p.index += 2
		case ch == '#':
			if p.needsCommand {
				return false
			}
			// PowerShell evaluates #requires before the script body and can load
			// modules or assemblies, so the whole script stays opaque.
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(p.lineCommentText())), "#requires") {
				return false
			}
			p.skipLineComment()
		case ch == '<' && p.index+1 < len(p.chars) && p.chars[p.index+1] == '#':
			if p.needsCommand {
				return false
			}
			end := p.blockCommentEnd()
			if end < 0 {
				return false
			}
			p.index = end
		case ch == '(' || ch == ')':
			// Sub-expressions and grouping need the PowerShell AST oracle.
			return false
		default:
			if !p.parseCommand() {
				return false
			}
			p.canChain = true
			p.needsCommand = false
		}
	}
	if p.needsCommand {
		return false
	}
	return true
}

func (p *powerShellCommandParser) skipLineComment() {
	for p.index < len(p.chars) && p.chars[p.index] != '\r' && p.chars[p.index] != '\n' {
		p.index++
	}
}

func (p *powerShellCommandParser) lineCommentText() string {
	end := p.index
	for end < len(p.chars) && p.chars[end] != '\r' && p.chars[end] != '\n' {
		end++
	}
	return string(p.chars[p.index:end])
}

func (p *powerShellCommandParser) blockCommentEnd() int {
	for index := p.index + 2; index+1 < len(p.chars); index++ {
		if p.chars[index] == '#' && p.chars[index+1] == '>' {
			return index + 2
		}
	}
	return -1
}

func (p *powerShellCommandParser) parseCommand() bool {
	words := make([]string, 0, 4)
	for {
		p.skipCommandWhitespace()
		if p.index >= len(p.chars) {
			break
		}
		ch := p.chars[p.index]
		if isPowerShellCommandSeparator(ch) || ch == '#' {
			break
		}
		word, ok := p.parseWord()
		if !ok {
			return false
		}
		if word == "" {
			return false
		}
		if !p.atWordBoundary() {
			return false
		}
		words = append(words, word)
	}
	if len(words) == 0 {
		return false
	}
	p.commands = append(p.commands, words)
	return true
}

// skipCommandWhitespace consumes horizontal whitespace only: a newline ends the
// current command.
func (p *powerShellCommandParser) skipCommandWhitespace() {
	for p.index < len(p.chars) {
		ch := p.chars[p.index]
		if ch == '\r' || ch == '\n' || !unicode.IsSpace(ch) {
			return
		}
		p.index++
	}
}

func isPowerShellCommandSeparator(ch rune) bool {
	switch ch {
	case '\r', '\n', ';', '|', '&', '(', ')':
		return true
	}
	return false
}

func (p *powerShellCommandParser) parseWord() (string, bool) {
	switch p.chars[p.index] {
	case '\'':
		return p.parseSingleQuoted()
	case '"':
		return p.parseDoubleQuoted()
	default:
		return p.parseBareWord()
	}
}

func (p *powerShellCommandParser) parseSingleQuoted() (string, bool) {
	var value strings.Builder
	index := p.index + 1
	for index < len(p.chars) {
		if p.chars[index] == '\'' {
			if index+1 < len(p.chars) && p.chars[index+1] == '\'' {
				value.WriteByte('\'')
				index += 2
				continue
			}
			p.index = index + 1
			return value.String(), true
		}
		value.WriteRune(p.chars[index])
		index++
	}
	return "", false
}

func (p *powerShellCommandParser) parseDoubleQuoted() (string, bool) {
	var value strings.Builder
	index := p.index + 1
	for index < len(p.chars) {
		ch := p.chars[index]
		switch ch {
		case '"':
			p.index = index + 1
			return value.String(), true
		case '$':
			return "", false
		case '`':
			if index+1 >= len(p.chars) {
				return "", false
			}
			escaped := p.chars[index+1]
			// `e is ESC only in PowerShell 6+, so it has no version-neutral value.
			if escaped == 'e' {
				return "", false
			}
			if escaped == 'u' && index+2 < len(p.chars) && p.chars[index+2] == '{' {
				return "", false
			}
			value.WriteRune(decodePowerShellBacktickEscape(escaped))
			index += 2
		default:
			value.WriteRune(ch)
			index++
		}
	}
	return "", false
}

func (p *powerShellCommandParser) parseBareWord() (string, bool) {
	var value strings.Builder
	for p.index < len(p.chars) {
		ch := p.chars[p.index]
		if unicode.IsSpace(ch) || isPowerShellCommandSeparator(ch) {
			break
		}
		if ch == '#' {
			if p.embeddedHashEndsToken() {
				return "", false
			}
			value.WriteRune(ch)
			p.index++
			continue
		}
		if ch == '`' {
			if p.index+1 >= len(p.chars) {
				return "", false
			}
			escaped := p.chars[p.index+1]
			if escaped == 'e' {
				return "", false
			}
			value.WriteRune(decodePowerShellBacktickEscape(escaped))
			p.index += 2
			continue
		}
		if isRejectedPowerShellBareRune(ch) {
			return "", false
		}
		value.WriteRune(ch)
		p.index++
	}
	word := value.String()
	if word == "" {
		return "", false
	}
	if !validPowerShellBareWord(word) {
		return "", false
	}
	return word, true
}

// embeddedHashEndsToken reports the tree-sitter recovery shape Rust rejects:
// an embedded `#` immediately followed by a token separator makes the parser
// split the comment out of the bare token.
func (p *powerShellCommandParser) embeddedHashEndsToken() bool {
	index := p.index + 1
	if index >= len(p.chars) {
		return true
	}
	ch := p.chars[index]
	if unicode.IsSpace(ch) {
		return true
	}
	return strings.ContainsRune("|&;#><(){}", ch)
}

func (p *powerShellCommandParser) atWordBoundary() bool {
	if p.index >= len(p.chars) {
		return true
	}
	ch := p.chars[p.index]
	return unicode.IsSpace(ch) || ch == '#' || isPowerShellCommandSeparator(ch)
}

func isRejectedPowerShellBareRune(ch rune) bool {
	switch ch {
	case '$', '@', '\'', '"', '(', ')', '{', '}', '[', ']', '>', '<', ',':
		return true
	}
	return false
}

func validPowerShellBareWord(word string) bool {
	// Attached parameter values need PowerShell-specific conversion.
	if strings.HasPrefix(word, "-") && !strings.HasPrefix(word, "--") && strings.Contains(word, ":") {
		return false
	}
	// tree-sitter rejects native single-dash `-flag=value` tokens; Rust only
	// masks the equals sign for the `--flag=value` form.
	if strings.HasPrefix(word, "-") && !strings.HasPrefix(word, "--") && strings.Contains(word, "=") {
		return false
	}
	// Keep only canonical decimal spellings: the runtime string is obviously
	// identical to the source spelling for those.
	if word[0] >= '0' && word[0] <= '9' {
		if word == "0" {
			return true
		}
		if word[0] == '0' {
			return false
		}
		for index := 0; index < len(word); index++ {
			if word[index] < '0' || word[index] > '9' {
				return false
			}
		}
	}
	return true
}

func decodePowerShellBacktickEscape(ch rune) rune {
	switch ch {
	case '0':
		return '\x00'
	case 'a':
		return '\a'
	case 'b':
		return '\b'
	case 'f':
		return '\f'
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	case 'v':
		return '\v'
	default:
		return ch
	}
}
