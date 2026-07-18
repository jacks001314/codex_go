package elevated

import (
	"strings"
)

func quoteWindowsArg(arg string) string {
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

func argvToCommandLine(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = quoteWindowsArg(arg)
	}
	return strings.Join(quoted, " ")
}
