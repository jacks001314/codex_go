package tea

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	bubbletea "charm.land/bubbletea/v2"
)

// Rust parity: codex-rs/tui/src/bottom_pane/chat_composer/slash_argument.rs
// (#62343 positional arguments, #63036 named settings).
//
// The slash menu completes command names, so a command with a small fixed set
// of first arguments also completes that argument: `/mcp lo` offers `login`.
// The named settings of `/model`, `/permissions`, `/title`, `/statusline` and
// `/mcp login` complete their flags and known values in both `--name=value`
// and `--name value` forms. Positional suggestions stay on the first line;
// named settings also work in multiline drafts. Tab revalidates the live token
// before rewriting it, while Enter and the configured submit keys still send
// the draft exactly as typed.

// slashCompletionProbe marks the cursor position while checking that the
// draft before it is a plain shell word boundary (Rust named::PROBE).
const slashCompletionProbe = "__codex_completion_probe__"

// slashArgumentChoices is Rust's `choices(SlashCommand)` table: commands whose
// first argument is a finite list.
func slashArgumentChoices(command string) []string {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "raw":
		return []string{"on", "off"}
	case "ide":
		return []string{"on", "off", "status"}
	case "usage":
		return []string{"daily", "weekly", "cumulative"}
	case "voice":
		return []string{"settings", "mute", "stop"}
	case "keymap":
		return []string{"debug"}
	case "goal":
		return []string{"clear", "edit", "pause", "resume"}
	case "mcp":
		return []string{"verbose", "login"}
	case "pets":
		return []string{"off"}
	}
	return nil
}

// slashArgumentTarget is the token the popup completes.
type slashArgumentTarget struct {
	Command string
	// Prefix is the text between the start of the token and the cursor.
	Prefix string
	// Start and End bound the token inside the composer text in bytes.
	Start int
	End   int
	// Choices are every value the token may become.
	Choices []string
	// Named marks a flag or value token from slash_command_settings.
	Named bool
}

// slashArgumentTargetFor resolves the token Tab rewrites. The first argument
// of a command with a finite table completes on the first line; everything
// else goes through the named settings, which also work in multiline drafts.
// Unknown commands, unquoted-but-hidden positions and commands the current
// thread cannot run yield no target (Rust slash_argument_target).
func (m *Model) slashArgumentTargetFor() (slashArgumentTarget, bool) {
	if m == nil {
		return slashArgumentTarget{}, false
	}
	text := m.composerText()
	cursor := composerByteOffset(text, m.composerCursor())
	firstLine := text
	onFirstLine := true
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		firstLine = text[:index]
		onFirstLine = cursor <= index
	}
	source := text
	if onFirstLine {
		source = firstLine
	}
	name, args, start, ok := parseSlashArgumentName(source)
	if !ok || cursor <= 1+len(name) || cursor < start {
		return slashArgumentTarget{}, false
	}
	if !m.slashArgumentCommandAvailable(name) {
		return slashArgumentTarget{}, false
	}
	end := start + len(args)
	for index, char := range args {
		if unicode.IsSpace(char) {
			end = start + index
			break
		}
	}
	if onFirstLine && cursor <= end {
		if choices := slashArgumentChoices(name); len(choices) > 0 {
			return slashArgumentTarget{
				Command: name,
				Prefix:  text[start:cursor],
				Start:   start,
				End:     end,
				Choices: choices,
			}, true
		}
	}
	return slashNamedArgumentTarget(text, cursor, start, name)
}

// composerByteOffset converts a rune offset in text to the byte offset that
// the Rust implementation's cursor uses.
func composerByteOffset(text string, offset int) int {
	if offset <= 0 {
		return 0
	}
	count := 0
	for index := range text {
		if count == offset {
			return index
		}
		count++
	}
	return len(text)
}

// parseSlashArgumentName mirrors Rust's parse_slash_name: the name ends at the
// first whitespace, args starts after it, and start is args' byte offset.
func parseSlashArgumentName(line string) (name, args string, start int, ok bool) {
	stripped, found := strings.CutPrefix(line, "/")
	if !found {
		return "", "", 0, false
	}
	nameEnd := len(stripped)
	for index, char := range stripped {
		if unicode.IsSpace(char) {
			nameEnd = index
			break
		}
	}
	name = stripped[:nameEnd]
	if name == "" {
		return "", "", 0, false
	}
	restUntrimmed := stripped[nameEnd:]
	args = strings.TrimLeftFunc(restUntrimmed, unicode.IsSpace)
	start = 1 + nameEnd + len(restUntrimmed) - len(args)
	return name, args, start, true
}

// slashNamedArgumentTarget mirrors slash_argument/named.rs: it locates the
// simple flag or value token at the cursor and returns every valid completion
// for it. Quoted, escaped and commented-out positions are left untouched.
func slashNamedArgumentTarget(text string, cursor, argsStart int, command string) (slashArgumentTarget, bool) {
	settings := slashSettingsForCommand(command)
	if settings.empty() {
		return slashArgumentTarget{}, false
	}
	start := argsStart
	if index := strings.LastIndexAny(text[argsStart:cursor], " \t\n"); index >= 0 {
		start = argsStart + index + 1
	}
	end := len(text)
	if index := strings.IndexAny(text[cursor:], " \t\n"); index >= 0 {
		end = cursor + index
	}
	token := text[start:end]
	editableEnd := len(token)
	if equal := strings.IndexByte(token, '='); equal >= 0 && cursor <= start+equal {
		editableEnd = equal
	}
	if strings.HasPrefix(token, "#") || strings.ContainsAny(token[:editableEnd], "'\"\\") {
		return slashArgumentTarget{}, false
	}
	prefix := text[argsStart:start]
	words, ok := splitSlashCompletionWords(prefix + slashCompletionProbe)
	if !ok || len(words) == 0 || words[len(words)-1] != slashCompletionProbe {
		return slashArgumentTarget{}, false
	}
	words = words[:len(words)-1]
	prefixWords, ok := splitSlashCompletionWords(prefix)
	if !ok || len(words) != len(prefixWords) {
		return slashArgumentTarget{}, false
	}
	if strings.EqualFold(strings.TrimSpace(command), "mcp") {
		subcommand, _, found := strings.Cut(strings.TrimLeftFunc(prefix, unicode.IsSpace), " ")
		if !found || !strings.EqualFold(subcommand, "login") || len(words) == 0 {
			return slashArgumentTarget{}, false
		}
		words = words[1:]
	}

	var used []string
	pending := ""
	hasPending := false
	positionals := 0
	for _, word := range words {
		if hasPending && !strings.HasPrefix(word, "--") {
			hasPending = false
			continue
		}
		hasPending = false
		if word == "--" {
			return slashArgumentTarget{}, false
		}
		if strings.HasPrefix(word, "--") {
			name := word
			inline := ""
			if equal := strings.IndexByte(word, '='); equal >= 0 {
				name, inline = word[:equal], word[equal+1:]
			}
			used = append(used, name)
			if inline == "" && slashArgsContains(settings.values, name) {
				pending, hasPending = name, true
			}
			continue
		}
		positionals++
	}

	tail, ok := splitSlashCompletionWords(text[start:])
	if !ok {
		return slashArgumentTarget{}, false
	}
	if token != "" {
		if len(tail) == 0 {
			return slashArgumentTarget{}, false
		}
		tail = tail[1:]
	}
	if token == "--" && len(tail) > 0 {
		return slashArgumentTarget{}, false
	}
	terminator := len(tail)
	for index, word := range tail {
		if word == "--" {
			terminator = index
			break
		}
	}
	if terminator < len(tail) {
		positionals += len(tail) - terminator - 1
	}
	for _, word := range tail[:terminator] {
		if strings.HasPrefix(word, "--") {
			name := word
			if equal := strings.IndexByte(word, '='); equal >= 0 {
				name = word[:equal]
			}
			used = append(used, name)
		} else if slashNamedPositionalCommand(command) {
			positionals++
		}
	}
	if positionals > 0 && slashNamedPositionalCommand(command) {
		used = append(used, "--clear")
	}

	valueTarget := func(name string, rangeStart, rangeEnd int) (slashArgumentTarget, bool) {
		ownUse := 0
		if hasPending && pending == name && !strings.Contains(token, "=") {
			ownUse = 1
		}
		occupied := rangeStart == rangeEnd && len(tail) > 0 && !strings.HasPrefix(tail[0], "--")
		if occupied || countSlashUsed(used, name) > ownUse {
			return slashArgumentTarget{}, false
		}
		choices := slashCommandValueChoices(command, name)
		if len(choices) == 0 {
			return slashArgumentTarget{}, false
		}
		return slashArgumentTarget{
			Command: command,
			Prefix:  text[rangeStart:cursor],
			Start:   rangeStart,
			End:     rangeEnd,
			Choices: choices,
			Named:   true,
		}, true
	}
	if hasPending && !strings.HasPrefix(token, "--") {
		return valueTarget(pending, start, end)
	}
	if !strings.HasPrefix(token, "-") {
		return slashArgumentTarget{}, false
	}
	if equal := strings.IndexByte(token, '='); equal >= 0 {
		if cursor > start+equal {
			return valueTarget(token[:equal], start+equal+1, end)
		}
		names := slashNamedOptionNames(settings.values, nil, nil, used)
		return slashNamedOptionTarget(command, names, start, start+equal, text, cursor)
	}
	names := slashNamedOptionNames(settings.values, settings.switches, settings.booleans, used)
	return slashNamedOptionTarget(command, names, start, end, text, cursor)
}

// slashNamedPositionalCommand reports commands whose plain words are
// positionals that rule out `--clear` (Rust Title and Statusline).
func slashNamedPositionalCommand(command string) bool {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "title", "statusline":
		return true
	}
	return false
}

// slashNamedOptionNames mirrors named::option_names: values and switches are
// offered as-is, each boolean expands to `--name` and `--no-name`, and flags
// already present in the command are filtered out.
func slashNamedOptionNames(values, switches, booleans, used []string) []string {
	names := make([]string, 0, len(values)+len(switches)+2*len(booleans))
	names = append(names, values...)
	names = append(names, switches...)
	for _, boolean := range booleans {
		positive, negative := "--"+boolean, "--no-"+boolean
		if !slashArgsContains(used, positive) && !slashArgsContains(used, negative) {
			names = append(names, positive, negative)
		}
	}
	filtered := names[:0]
	for _, name := range names {
		if !slashArgsContains(used, name) {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

func slashNamedOptionTarget(command string, names []string, start, end int, text string, cursor int) (slashArgumentTarget, bool) {
	if len(names) == 0 {
		return slashArgumentTarget{}, false
	}
	return slashArgumentTarget{
		Command: command,
		Prefix:  text[start:cursor],
		Start:   start,
		End:     end,
		Choices: names,
		Named:   true,
	}, true
}

func countSlashUsed(used []string, name string) int {
	count := 0
	for _, candidate := range used {
		if candidate == name {
			count++
		}
	}
	return count
}

// splitSlashCompletionWords mirrors Rust's shlex::split, which the probe and
// the token scan rely on: ASCII whitespace separates words, `#` starts a
// comment at the start of a word, quotes group text, and an unterminated quote
// or trailing backslash is an error.
func splitSlashCompletionWords(input string) ([]string, bool) {
	var words []string
	index := 0
	for {
		for index < len(input) {
			switch input[index] {
			case ' ', '\t', '\n':
				index++
				continue
			case '#':
				for index < len(input) && input[index] != '\n' {
					index++
				}
				continue
			}
			break
		}
		if index >= len(input) {
			return words, true
		}
		var word strings.Builder
	word:
		for index < len(input) {
			switch char := input[index]; char {
			case ' ', '\t', '\n':
				break word
			case '"':
				index++
				closed := false
				for index < len(input) {
					if input[index] == '\\' {
						if index+1 >= len(input) {
							return nil, false
						}
						next := input[index+1]
						switch next {
						case '$', '`', '"', '\\':
							word.WriteByte(next)
							index += 2
						case '\n':
							index += 2
						default:
							word.WriteByte('\\')
							word.WriteByte(next)
							index += 2
						}
						continue
					}
					if input[index] == '"' {
						index++
						closed = true
						break
					}
					word.WriteByte(input[index])
					index++
				}
				if !closed {
					return nil, false
				}
			case '\'':
				index++
				start := index
				for index < len(input) && input[index] != '\'' {
					index++
				}
				if index >= len(input) {
					return nil, false
				}
				word.WriteString(input[start:index])
				index++
			case '\\':
				if index+1 >= len(input) {
					return nil, false
				}
				if input[index+1] != '\n' {
					word.WriteByte(input[index+1])
				}
				index += 2
			default:
				word.WriteByte(char)
				index++
			}
		}
		words = append(words, word.String())
	}
}

// slashArgumentCommandAvailable reports whether the command is in the current
// catalog and can run here (Rust limits suggestions to builtins available for
// the thread and connection state).
func (m *Model) slashArgumentCommandAvailable(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, item := range m.slashPopupCatalog() {
		if strings.EqualFold(strings.TrimSpace(item.Name), name) {
			return !item.Unavailable
		}
	}
	return false
}

// slashArgumentIdentity mirrors Rust's Target::identity: the token start plus
// the whole draft, so the popup only stays dismissed for the exact draft.
func slashArgumentIdentity(target slashArgumentTarget, text string) string {
	return fmt.Sprintf("%d:%s", target.Start, text)
}

// refreshSlashArgumentPopup replaces the command menu with the argument
// suggestions for the current draft. Restricted input keeps the command menu
// closed instead (Rust returns no popup while disconnected or in draft
// handoff).
func (m *Model) refreshSlashArgumentPopup() bool {
	target, ok := m.slashArgumentTargetFor()
	if !ok || m.threadUnavailable || m.appServerDisconnected {
		m.slashArgumentDismissed = ""
		return false
	}
	identity := slashArgumentIdentity(target, m.composerText())
	if m.slashArgumentDismissed != "" && m.slashArgumentDismissed == identity {
		return false
	}
	m.slashArgumentDismissed = ""
	prefix := strings.ToLower(target.Prefix)
	items := make([]slashCommandPopupItem, 0, len(target.Choices))
	for _, choice := range target.Choices {
		if prefix != "" && !strings.HasPrefix(strings.ToLower(choice), prefix) {
			continue
		}
		items = append(items, slashCommandPopupItem{Name: choice})
	}
	if len(items) == 0 {
		m.slashPopup = slashCommandPopup{}
		return true
	}
	selected := 0
	if m.slashPopup.Argument && strings.EqualFold(m.slashPopup.ArgumentCommand, target.Command) {
		if previous := m.slashPopup.Selected; previous >= 0 && previous < len(items) {
			selected = previous
		}
	}
	m.slashPopup = slashCommandPopup{
		Active:          true,
		Query:           target.Prefix,
		Items:           items,
		Selected:        selected,
		Argument:        true,
		ArgumentNamed:   target.Named,
		ArgumentCommand: target.Command,
		ArgumentStart:   target.Start,
		ArgumentEnd:     target.End,
	}
	return true
}

// completeSlashArgument rewrites the live token with the selected suggestion.
// The token is re-validated first, so a draft edited after the popup was built
// is left alone (Rust Tab revalidates the live token).
func (m *Model) completeSlashArgument() bubbletea.Cmd {
	if m == nil || !m.slashPopup.Argument {
		return nil
	}
	item, ok := m.currentSlashPopupItem()
	if !ok {
		return nil
	}
	target, ok := m.slashArgumentTargetFor()
	if !ok ||
		target.Start != m.slashPopup.ArgumentStart ||
		target.End != m.slashPopup.ArgumentEnd ||
		target.Prefix != m.slashPopup.Query ||
		!slashArgsContains(target.Choices, item.Name) {
		m.slashPopup = slashCommandPopup{}
		return nil
	}
	text := m.composerText()
	m.setComposerPrompt(text[:target.Start] + item.Name + text[target.End:])
	m.setComposerCursorToByte(target.Start + len(item.Name))
	m.slashArgumentDismissed = ""
	m.slashPopup = slashCommandPopup{}
	return nil
}

// setComposerCursorToByte moves the textarea cursor to a byte offset in
// canonical composer text. Rust leaves the cursor after the inserted value.
func (m *Model) setComposerCursorToByte(offset int) {
	if m == nil {
		return
	}
	body := m.composerBody()
	bodyOffset := offset - len(m.composerPrefix.text())
	if bodyOffset < 0 {
		bodyOffset = 0
	}
	if bodyOffset > len(body) {
		bodyOffset = len(body)
	}
	for bodyOffset > 0 && bodyOffset < len(body) && !utf8.RuneStart(body[bodyOffset]) {
		bodyOffset--
	}
	m.setComposerCursorOffset(utf8.RuneCountInString(body[:bodyOffset]))
}
