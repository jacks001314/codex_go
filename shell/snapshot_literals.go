package shell

import "strings"

// SnapshotLiteralWords decodes the literal words a POSIX shell would rebuild
// from a rendered snapshot script after quote removal. Credential policy uses
// it to find a secret that an alias or function body reassembles from quoted
// fragments, which a plain text replacement cannot see.
//
// Rust scans the same script with tree-sitter
// (shell-command/src/shell_snapshot_literals.rs). This scan is lexical: it
// decodes single quotes, double-quote escapes, backslash escapes, ANSI-C
// $'...' quoting, and fragments concatenated without separation, and it scans
// backtick and $(...) bodies as scripts of their own. Constructs it cannot
// decode (heredoc bodies, parameter-expansion operators) stay opaque, so the
// scan can under-report what a shell will execute but never invents a word
// from unrelated pieces of the script.
func SnapshotLiteralWords(shellType ShellType, script string) []string {
	// Bash invoked as sh still parses its array and alias syntax.
	if shellType == ShellSh && strings.HasPrefix(script, bashShSnapshotHeader) {
		shellType = ShellBash
	}
	switch shellType {
	case ShellBash, ShellSh, ShellZsh:
	default:
		return nil
	}
	var words []string
	scanSnapshotLiteralWords(shellType, script, &words)
	return words
}

func scanSnapshotLiteralWords(shellType ShellType, script string, words *[]string) {
	var word strings.Builder
	inWord := false
	flush := func() {
		if !inWord {
			return
		}
		*words = append(*words, word.String())
		word.Reset()
		inWord = false
	}
	appendText := func(text string) {
		word.WriteString(text)
		inWord = true
	}
	for index := 0; index < len(script); {
		switch character := script[index]; character {
		case '\\':
			if index+1 >= len(script) {
				appendText("\\")
				index++
				continue
			}
			if script[index+1] == '\n' {
				index += 2
				continue
			}
			appendText(script[index+1 : index+2])
			index += 2
		case '\'':
			end := strings.IndexByte(script[index+1:], '\'')
			if end < 0 {
				appendText(script[index+1:])
				index = len(script)
				continue
			}
			appendText(script[index+1 : index+1+end])
			index += end + 2
		case '"':
			text, nested, next := decodeSnapshotDoubleQuote(script, index+1)
			appendText(text)
			for _, body := range nested {
				flush()
				scanSnapshotLiteralWords(shellType, body, words)
			}
			index = next
		case '`':
			body, next := decodeSnapshotBacktick(script, index+1)
			flush()
			scanSnapshotLiteralWords(shellType, body, words)
			index = next
		case '$':
			switch {
			case strings.HasPrefix(script[index:], "$'"):
				text, next := decodeSnapshotANSIQuote(shellType, script, index+2)
				appendText(text)
				index = next
			case strings.HasPrefix(script[index:], "$("):
				body, next := decodeSnapshotCommandSubstitution(script, index+2)
				flush()
				scanSnapshotLiteralWords(shellType, body, words)
				index = next
			default:
				appendText("$")
				index++
			}
		case '#':
			if inWord {
				appendText("#")
				index++
				continue
			}
			if next := strings.IndexByte(script[index:], '\n'); next >= 0 {
				index += next
			} else {
				index = len(script)
			}
		case ' ', '\t', '\r', '\n', ';', '|', '&', '<', '>', '(', ')':
			flush()
			index++
		default:
			appendText(script[index : index+1])
			index++
		}
	}
	flush()
}

// decodeSnapshotDoubleQuote decodes one double-quoted region and returns the
// command substitutions it embeds, which the shell parses as separate scripts.
func decodeSnapshotDoubleQuote(script string, index int) (string, []string, int) {
	var text strings.Builder
	var nested []string
	for index < len(script) {
		switch character := script[index]; character {
		case '"':
			return text.String(), nested, index + 1
		case '\\':
			if index+1 >= len(script) {
				text.WriteByte('\\')
				index++
				continue
			}
			switch next := script[index+1]; next {
			case '$', '`', '"', '\\':
				text.WriteByte(next)
				index += 2
			case '\n':
				index += 2
			default:
				// A backslash only escapes those four characters inside quotes.
				text.WriteByte('\\')
				index++
			}
		case '$':
			if strings.HasPrefix(script[index:], "$(") {
				body, next := decodeSnapshotCommandSubstitution(script, index+2)
				nested = append(nested, body)
				index = next
				continue
			}
			text.WriteByte(character)
			index++
		case '`':
			body, next := decodeSnapshotBacktick(script, index+1)
			nested = append(nested, body)
			index = next
		default:
			text.WriteByte(character)
			index++
		}
	}
	return text.String(), nested, index
}

// decodeSnapshotBacktick returns a backtick body with one escape layer
// removed, which is how the shell hands the body to its own parser.
func decodeSnapshotBacktick(script string, index int) (string, int) {
	var text strings.Builder
	for index < len(script) {
		switch character := script[index]; character {
		case '`':
			return text.String(), index + 1
		case '\\':
			if index+1 >= len(script) {
				text.WriteByte('\\')
				index++
				continue
			}
			switch next := script[index+1]; next {
			case '\n':
				index += 2
			case '$', '`', '\\':
				text.WriteByte(next)
				index += 2
			default:
				text.WriteByte('\\')
				index++
			}
		default:
			text.WriteByte(character)
			index++
		}
	}
	return text.String(), index
}

// decodeSnapshotCommandSubstitution returns the body of a $() substitution and
// the index just past its closing parenthesis, honoring nested quotes.
func decodeSnapshotCommandSubstitution(script string, index int) (string, int) {
	start := index
	depth := 0
	for index < len(script) {
		switch script[index] {
		case '\\':
			index += 2
		case '\'':
			end := strings.IndexByte(script[index+1:], '\'')
			if end < 0 {
				return script[start:], len(script)
			}
			index += end + 2
		case '"':
			if index+1 >= len(script) {
				return script[start:], len(script)
			}
			_, _, next := decodeSnapshotDoubleQuote(script, index+1)
			index = next
		case '`':
			_, next := decodeSnapshotBacktick(script, index+1)
			index = next
		case '(':
			depth++
			index++
		case ')':
			if depth == 0 {
				return script[start:index], index + 1
			}
			depth--
			index++
		default:
			index++
		}
	}
	return script[start:], index
}

// decodeSnapshotANSIQuote decodes $'...' quoting, where escapes are expanded
// before the word reaches the command.
func decodeSnapshotANSIQuote(shellType ShellType, script string, index int) (string, int) {
	var text strings.Builder
	for index < len(script) {
		character := script[index]
		if character == '\'' {
			return text.String(), index + 1
		}
		if character != '\\' || index+1 >= len(script) {
			text.WriteByte(character)
			index++
			continue
		}
		escape := script[index+1]
		index += 2
		switch escape {
		case 'a':
			text.WriteByte(0x07)
		case 'b':
			text.WriteByte(0x08)
		case 'e', 'E':
			text.WriteByte(0x1b)
		case 'f':
			text.WriteByte(0x0c)
		case 'n':
			text.WriteByte('\n')
		case 'r':
			text.WriteByte('\r')
		case 't':
			text.WriteByte('\t')
		case 'v':
			text.WriteByte(0x0b)
		case '\\', '\'', '"', '?':
			text.WriteByte(escape)
		case 'c':
			if index < len(script) {
				text.WriteByte(script[index] & 0x1f)
				index++
			}
		case 'x', 'u', 'U':
			digits := 2
			switch escape {
			case 'u':
				digits = 4
			case 'U':
				digits = 8
			}
			value, consumed := parseSnapshotDigits(script[index:], 16, digits)
			if consumed == 0 {
				text.WriteByte('\\')
				text.WriteByte(escape)
				continue
			}
			index += consumed
			if escape == 'x' {
				text.WriteByte(byte(value))
				continue
			}
			text.WriteRune(rune(value))
		case '0', '1', '2', '3', '4', '5', '6', '7':
			value, consumed := parseSnapshotDigits(script[index-1:], 8, 3)
			index += consumed - 1
			text.WriteByte(byte(value))
		default:
			// Bash keeps the backslash of an escape it does not know; zsh drops it.
			if shellType != ShellZsh {
				text.WriteByte('\\')
			}
			text.WriteByte(escape)
		}
	}
	return text.String(), index
}

func parseSnapshotDigits(text string, base int, limit int) (int, int) {
	value := 0
	consumed := 0
	for consumed < len(text) && consumed < limit {
		digit := snapshotDigitValue(text[consumed])
		if digit < 0 || digit >= base {
			break
		}
		value = value*base + digit
		consumed++
	}
	return value, consumed
}

func snapshotDigitValue(character byte) int {
	switch {
	case character >= '0' && character <= '9':
		return int(character - '0')
	case character >= 'a' && character <= 'f':
		return int(character-'a') + 10
	case character >= 'A' && character <= 'F':
		return int(character-'A') + 10
	default:
		return -1
	}
}
