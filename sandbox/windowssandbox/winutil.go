package windowssandbox

import (
	"strings"
	"unicode/utf16"
)

func QuoteWindowsArg(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\n\r\"") {
		return arg
	}
	var quoted strings.Builder
	quoted.Grow(len(arg) + 2)
	quoted.WriteByte('"')
	backslashes := 0
	for _, ch := range arg {
		switch ch {
		case '\\':
			backslashes++
		case '"':
			quoted.WriteString(strings.Repeat(`\`, backslashes*2+1))
			quoted.WriteRune('"')
			backslashes = 0
		default:
			if backslashes > 0 {
				quoted.WriteString(strings.Repeat(`\`, backslashes))
				backslashes = 0
			}
			quoted.WriteRune(ch)
		}
	}
	if backslashes > 0 {
		quoted.WriteString(strings.Repeat(`\`, backslashes*2))
	}
	quoted.WriteByte('"')
	return quoted.String()
}

// CmdScriptCommandLine builds the native command line for Codex's
// `cmd.exe /c SCRIPT` invocation. The script is shell syntax, so CRT argument
// escaping would add literal backslashes before its quotes. With /s, cmd
// strips exactly the outer pair and preserves the script's own quotes
// (Rust #60624).
func CmdScriptCommandLine(argv []string) (string, bool) {
	if len(argv) != 3 {
		return "", false
	}
	program, switchArg, script := argv[0], argv[1], argv[2]
	name := program
	if index := strings.LastIndexAny(name, `/\`); index >= 0 {
		name = name[index+1:]
	}
	if !(strings.EqualFold(name, "cmd.exe") || strings.EqualFold(name, "cmd")) ||
		!strings.EqualFold(switchArg, "/c") ||
		strings.ContainsAny(program, "\"\x00") ||
		strings.ContainsRune(script, 0) {
		return "", false
	}
	return `"` + program + `" /s /c "` + script + `"`, true
}

func ArgvToCommandLine(argv []string) string {
	if commandLine, ok := CmdScriptCommandLine(argv); ok {
		return commandLine
	}
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = QuoteWindowsArg(arg)
	}
	return strings.Join(quoted, " ")
}

func ToWide(value string) []uint16 {
	out := utf16.Encode([]rune(value))
	return append(out, 0)
}
