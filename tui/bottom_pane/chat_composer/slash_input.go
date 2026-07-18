package chatcomposer

import (
	"strings"
	"unicode"
	"unicode/utf8"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/turn"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/chat_composer/slash_input.rs.

type SlashValidation int

const (
	SlashValidationImmediate SlashValidation = iota
	SlashValidationDeferred
)

type SubmissionValidationKind string

const (
	SubmissionValidationValid          SubmissionValidationKind = "valid"
	SubmissionValidationUnknownCommand SubmissionValidationKind = "unknown_command"
)

type SubmissionValidation struct {
	Kind           SubmissionValidationKind
	UnknownCommand string
}

func ValidSubmission() SubmissionValidation {
	return SubmissionValidation{Kind: SubmissionValidationValid}
}

func UnknownCommandSubmission(command string) SubmissionValidation {
	return SubmissionValidation{Kind: SubmissionValidationUnknownCommand, UnknownCommand: command}
}

func (v SubmissionValidation) Valid() bool {
	return v.Kind == "" || v.Kind == SubmissionValidationValid
}

type InlineCommand struct {
	Command    bottompane.SlashCommandItem
	Rest       string
	RestOffset int
}

type TextRange struct {
	Start int
	End   int
}

type SlashInput struct {
	Enabled             bool
	IsBashMode          bool
	CommandFlags        bottompane.BuiltinCommandFlags
	ServiceTierCommands []bottompane.ServiceTierCommand
}

func NewSlashInput(enabled bool, isBashMode bool, commandFlags bottompane.BuiltinCommandFlags, serviceTierCommands []bottompane.ServiceTierCommand) SlashInput {
	return SlashInput{
		Enabled:             enabled,
		IsBashMode:          isBashMode,
		CommandFlags:        commandFlags,
		ServiceTierCommands: append([]bottompane.ServiceTierCommand(nil), serviceTierCommands...),
	}
}

func IsSlashInput(text string) bool {
	return !strings.HasPrefix(text, " ") && strings.HasPrefix(strings.TrimSpace(text), "/")
}

func (s SlashInput) ValidateSubmission(text string, inputStartsWithSpace bool) SubmissionValidation {
	if !s.Enabled {
		return ValidSubmission()
	}
	parsed := bottompane.ParseSlashName(text)
	if !parsed.OK {
		return ValidSubmission()
	}
	if inputStartsWithSpace || strings.Contains(parsed.Name, "/") {
		return ValidSubmission()
	}
	if _, ok := s.command(parsed.Name); ok {
		return ValidSubmission()
	}
	return UnknownCommandSubmission(parsed.Name)
}

func (s SlashInput) BareCommand(text string) (bottompane.SlashCommandItem, bool) {
	if !s.Enabled || s.IsBashMode {
		return bottompane.SlashCommandItem{}, false
	}
	firstLine := text
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	parsed := bottompane.ParseSlashName(firstLine)
	if !parsed.OK || parsed.Rest != "" {
		return bottompane.SlashCommandItem{}, false
	}
	command, ok := s.command(parsed.Name)
	if !ok {
		return bottompane.SlashCommandItem{}, false
	}
	if command.SupportsInlineArgs() {
		full := bottompane.ParseSlashName(text)
		if full.OK && full.Rest != "" {
			return bottompane.SlashCommandItem{}, false
		}
	}
	return command, true
}

func (s SlashInput) InlineCommand(text string) (InlineCommand, bool) {
	if !s.Enabled || s.IsBashMode || strings.HasPrefix(text, " ") {
		return InlineCommand{}, false
	}
	parsed := bottompane.ParseSlashName(text)
	if !parsed.OK || parsed.Rest == "" || strings.Contains(parsed.Name, "/") {
		return InlineCommand{}, false
	}
	command, ok := s.command(parsed.Name)
	if !ok || !command.SupportsInlineArgs() {
		return InlineCommand{}, false
	}
	return InlineCommand{Command: command, Rest: parsed.Rest, RestOffset: parsed.RestOffset}, true
}

func (s SlashInput) ShouldParseOnDequeue(text string) bool {
	return s.Enabled && !strings.HasPrefix(text, " ") && strings.HasPrefix(strings.TrimSpace(text), "/")
}

func (s SlashInput) CommandElementRange(firstLine string, cursor int) (TextRange, bool) {
	if s.IsBashMode {
		return TextRange{}, false
	}
	parsed := bottompane.ParseSlashName(firstLine)
	if !parsed.OK || strings.Contains(parsed.Name, "/") {
		return TextRange{}, false
	}
	elementEnd := 1 + len(parsed.Name)
	if cursor <= len(firstLine) && cursor > 1 && cursor < elementEnd {
		return TextRange{}, false
	}
	if elementEnd >= len(firstLine) {
		return TextRange{}, false
	}
	next, _ := utf8.DecodeRuneInString(firstLine[elementEnd:])
	if !unicode.IsSpace(next) {
		return TextRange{}, false
	}
	if _, ok := s.command(parsed.Name); !ok {
		return TextRange{}, false
	}
	return TextRange{Start: 0, End: elementEnd}, true
}

func (s SlashInput) IsEditingCommandName(firstLine string, cursor int) bool {
	name, rest, ok := commandUnderCursor(firstLine, cursor)
	if !ok || !s.Enabled {
		return false
	}
	if name == "" {
		return rest == ""
	}
	return bottompane.HasSlashCommandPrefix(name, s.CommandFlags, s.ServiceTierCommands)
}

func (s SlashInput) CommandPopup(filterText string) *bottompane.CommandPopup {
	popup := bottompane.NewCommandPopup(bottompane.CommandPopupFlags{
		CollaborationModesEnabled:    s.CommandFlags.CollaborationModesEnabled,
		ConnectorsEnabled:            s.CommandFlags.ConnectorsEnabled,
		PluginsCommandEnabled:        s.CommandFlags.PluginsCommandEnabled,
		TokenActivityCommandEnabled:  s.CommandFlags.TokenActivityCommandEnabled,
		ServiceTierCommandsEnabled:   s.CommandFlags.ServiceTierCommandsEnabled,
		GoalCommandEnabled:           s.CommandFlags.GoalCommandEnabled,
		PersonalityCommandEnabled:    s.CommandFlags.PersonalityCommandEnabled,
		WindowsDegradedSandboxActive: s.CommandFlags.AllowElevateSandbox,
		SideConversationActive:       s.CommandFlags.SideConversationActive,
	}, s.ServiceTierCommands)
	popup.OnComposerTextChange(filterText)
	return popup
}

func (s SlashInput) command(name string) (bottompane.SlashCommandItem, bool) {
	return bottompane.FindSlashCommand(name, s.CommandFlags, s.ServiceTierCommands)
}

type QueuedInputAction string

const (
	QueuedInputPlain      QueuedInputAction = "plain"
	QueuedInputParseSlash QueuedInputAction = "parse_slash"
	QueuedInputRunShell   QueuedInputAction = "run_shell"
)

func QueuedInputActionFor(preparedText string, deferSlashValidation bool) QueuedInputAction {
	if deferSlashValidation && strings.HasPrefix(preparedText, "/") {
		return QueuedInputParseSlash
	}
	if strings.HasPrefix(preparedText, "!") {
		return QueuedInputRunShell
	}
	return QueuedInputPlain
}

func SelectedCommandDispatchesImmediatelyOnTab(command bottompane.SlashCommandItem) bool {
	return command.Kind == bottompane.SlashCommandItemBuiltin && command.Command == codextui.CommandSkills
}

func SelectedCommandCompletion(firstLine string, command bottompane.SlashCommandItem) (string, bool) {
	selectedCommandText := "/" + command.CommandText()
	if strings.HasPrefix(strings.TrimLeftFunc(firstLine, unicode.IsSpace), selectedCommandText) {
		return "", false
	}
	return selectedCommandText + " ", true
}

func PreparedArgs(preparedText string) (string, int, bool) {
	parsed := bottompane.ParseSlashName(preparedText)
	if !parsed.OK {
		return "", 0, false
	}
	return parsed.Rest, parsed.RestOffset, true
}

func ArgsElements(rest string, restOffset int, textElements []turn.TextElement) []turn.TextElement {
	if rest == "" || len(textElements) == 0 {
		return nil
	}
	out := make([]turn.TextElement, 0, len(textElements))
	for _, elem := range textElements {
		elemStart := int(elem.ByteRange.Start)
		elemEnd := int(elem.ByteRange.End)
		if elemEnd <= restOffset {
			continue
		}
		start := elemStart - restOffset
		if start < 0 {
			start = 0
		}
		end := elemEnd - restOffset
		if end > len(rest) {
			end = len(rest)
		}
		if start >= len(rest) || start >= end {
			continue
		}
		elem.ByteRange = turn.ByteRange{Start: uint(start), End: uint(end)}
		out = append(out, elem)
	}
	return out
}

func CommandPopupFilterText(firstLine string, cursor int) (string, bool) {
	name, _, ok := commandUnderCursor(firstLine, cursor)
	if !ok {
		return "", false
	}
	return "/" + name, true
}

func commandUnderCursor(firstLine string, cursor int) (string, string, bool) {
	if !strings.HasPrefix(firstLine, "/") || cursor > len(firstLine) || !isByteBoundary(firstLine, cursor) {
		return "", "", false
	}
	nameStart := 1
	nameEnd := len(firstLine)
	for idx, r := range firstLine[nameStart:] {
		if unicode.IsSpace(r) {
			nameEnd = nameStart + idx
			break
		}
	}
	effectiveCursor := cursor
	if effectiveCursor <= nameStart {
		effectiveCursor = nameEnd
	}
	if effectiveCursor > nameEnd {
		return "", "", false
	}
	return firstLine[nameStart:effectiveCursor], firstLine[effectiveCursor:], true
}

func isByteBoundary(text string, idx int) bool {
	if idx < 0 || idx > len(text) {
		return false
	}
	return idx == 0 || idx == len(text) || utf8.RuneStart(text[idx])
}
