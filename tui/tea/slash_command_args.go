package tea

import (
	"strings"
	"unicode"
)

// Shared slash-command tokenization (Rust #54385, upstream 36803b180a:
// codex-rs/tui/src/chatwidget/slash_command_args.rs). `/model` and `/permissions`
// share it so quoted values, `--setting=value`, `--setting value`, `--` and the
// rejection messages behave identically.
//
// The Rust parser splits with `shlex`, so the Go port carries a small POSIX-style
// splitter with the same observable behaviour: whitespace separates words, single
// quotes are literal, double quotes keep whitespace but honour backslash escapes,
// and an unterminated quote is an error.

type namedArgumentKind int

const (
	namedArgumentValue namedArgumentKind = iota
	namedArgumentSwitch
	namedArgumentBoolean
)

type namedArgument struct {
	name    string
	kind    namedArgumentKind
	value   string
	boolean bool
}

type parsedSlashCommandArgs struct {
	positionals []string
	named       []namedArgument
}

// value returns the string value of a value setting (Rust
// ParsedCommandArgs::value).
func (p parsedSlashCommandArgs) value(name string) (string, bool) {
	for _, argument := range p.named {
		if argument.name != name {
			continue
		}
		if argument.kind != namedArgumentValue {
			return "", false
		}
		return argument.value, true
	}
	return "", false
}

// hasSwitch reports a switch (Rust ParsedCommandArgs::has_switch).
func (p parsedSlashCommandArgs) hasSwitch(name string) bool {
	for _, argument := range p.named {
		if argument.name == name && argument.kind == namedArgumentSwitch {
			return true
		}
	}
	return false
}

// boolean returns a boolean setting's value (Rust ParsedCommandArgs::boolean).
// The name is the bare setting name; Rust prefixes it with `--`.
func (p parsedSlashCommandArgs) boolean(name string) (bool, bool) {
	full := "--" + name
	for _, argument := range p.named {
		if argument.name != full {
			continue
		}
		if argument.kind != namedArgumentBoolean {
			return false, false
		}
		return argument.boolean, true
	}
	return false, false
}

// parseSlashCommandArgs mirrors slash_command_args::parse. The returned string is
// the Rust error message, empty on success.
func parseSlashCommandArgs(args string, valueSettings []string, switches []string, booleanSettings []string) (parsedSlashCommandArgs, string) {
	words, err := splitCommandWords(args)
	if err != "" {
		return parsedSlashCommandArgs{}, err
	}
	var parsed parsedSlashCommandArgs
	parseSettings := true
	for index := 0; index < len(words); index++ {
		word := words[index]
		switch {
		case parseSettings && word == "--":
			parseSettings = false
		case parseSettings && strings.HasPrefix(word, "--"):
			argument, consumed, failure := parseNamedArgument(word, index, words, valueSettings, switches, booleanSettings)
			if failure != "" {
				return parsedSlashCommandArgs{}, failure
			}
			index = consumed
			for _, existing := range parsed.named {
				if existing.name != argument.name {
					continue
				}
				if existing.kind == namedArgumentBoolean && argument.kind == namedArgumentBoolean && existing.boolean != argument.boolean {
					name := strings.TrimPrefix(argument.name, "--")
					return parsedSlashCommandArgs{}, "Specify only one of --" + name + " or --no-" + name + "."
				}
				return parsedSlashCommandArgs{}, "Command setting `" + argument.name + "` was provided more than once."
			}
			parsed.named = append(parsed.named, argument)
		default:
			parsed.positionals = append(parsed.positionals, word)
		}
	}
	return parsed, ""
}

// parseNamedArgument mirrors slash_command_args::parse_named_argument. consumed
// is the index of the last token the argument took (the value when the setting
// consumed the next word).
func parseNamedArgument(word string, index int, words []string, valueSettings []string, switches []string, booleanSettings []string) (namedArgument, int, string) {
	name := word
	inlineValue := ""
	hasInlineValue := false
	if at := strings.IndexByte(word, '='); at >= 0 {
		name = word[:at]
		inlineValue = word[at+1:]
		hasInlineValue = true
	}
	if slashArgsContains(valueSettings, name) {
		value := ""
		consumed := index
		switch {
		case hasInlineValue && inlineValue != "":
			value = inlineValue
		case !hasInlineValue && index+1 < len(words) && !strings.HasPrefix(words[index+1], "--"):
			value = words[index+1]
			consumed = index + 1
		}
		if value == "" {
			return namedArgument{}, index, "Command setting `" + name + "` requires a value."
		}
		return namedArgument{name: name, kind: namedArgumentValue, value: value}, consumed, ""
	}
	if slashArgsContains(switches, name) {
		if hasInlineValue {
			return namedArgument{}, index, "Command setting `" + name + "` does not take a value."
		}
		return namedArgument{name: name, kind: namedArgumentSwitch}, index, ""
	}
	for _, setting := range booleanSettings {
		positive := "--" + setting
		negative := "--no-" + setting
		if name != positive && name != negative {
			continue
		}
		if hasInlineValue {
			return namedArgument{}, index, "Command setting `" + name + "` does not take a value."
		}
		return namedArgument{name: positive, kind: namedArgumentBoolean, boolean: name != negative}, index, ""
	}
	return namedArgument{}, index, "Unknown command setting `" + name + "`."
}

func slashArgsContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// splitCommandWords is the shlex-equivalent word splitter. The returned string is
// the Rust parser's error message for invalid quoting.
func splitCommandWords(args string) ([]string, string) {
	const invalidQuoting = "Invalid command arguments. Check the quoting."
	var words []string
	var current strings.Builder
	started := false
	runes := []rune(args)
	for index := 0; index < len(runes); index++ {
		char := runes[index]
		switch {
		case unicode.IsSpace(char):
			if started {
				words = append(words, current.String())
				current.Reset()
				started = false
			}
		case char == '\'':
			started = true
			end := index + 1
			for end < len(runes) && runes[end] != '\'' {
				end++
			}
			if end >= len(runes) {
				return nil, invalidQuoting
			}
			current.WriteString(string(runes[index+1 : end]))
			index = end
		case char == '"':
			started = true
			end := index + 1
			for end < len(runes) && runes[end] != '"' {
				if runes[end] == '\\' && end+1 < len(runes) {
					escaped := runes[end+1]
					if escaped == '"' || escaped == '\\' || escaped == '$' || escaped == '`' {
						current.WriteRune(escaped)
						end += 2
						continue
					}
				}
				current.WriteRune(runes[end])
				end++
			}
			if end >= len(runes) {
				return nil, invalidQuoting
			}
			index = end
		case char == '\\':
			started = true
			if index+1 >= len(runes) {
				return nil, invalidQuoting
			}
			current.WriteRune(runes[index+1])
			index++
		default:
			started = true
			current.WriteRune(char)
		}
	}
	if started {
		words = append(words, current.String())
	}
	return words, ""
}
