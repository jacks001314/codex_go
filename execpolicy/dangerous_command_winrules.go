package execpolicy

import (
	"net/url"
	"regexp"
	"strings"
)

// isDangerousCommandWindows mirrors codex-shell-command's Windows dangerous
// command classifier (Rust #42113). Windows rules are evaluated from the
// command words alone, so they also apply to a remote Windows executor driven
// from a POSIX host.
func isDangerousCommandWindows(command []string) bool {
	if isDangerousPowerShell(command) {
		return true
	}
	if isDangerousCmd(command) {
		return true
	}
	return isDirectGUILaunch(command)
}

func isDangerousPowerShell(command []string) bool {
	if len(command) == 0 {
		return false
	}
	if !isPowerShellExecutable(command[0]) {
		return false
	}
	tokens, ok := parsePowerShellInvocation(command[1:])
	if !ok {
		return false
	}
	return isDangerousPowerShellWords(tokens)
}

func isDangerousPowerShellWords(words []string) bool {
	tokens := make([]string, 0, len(words))
	for _, word := range words {
		tokens = append(tokens, strings.ToLower(strings.Trim(strings.Trim(word, "'"), `"`)))
	}
	hasURL := argsHaveURL(words)

	if hasURL && containsAnyToken(tokens, func(token string) bool {
		switch token {
		case "start-process", "start", "saps", "invoke-item", "ii":
			return true
		}
		return strings.Contains(token, "start-process") || strings.Contains(token, "invoke-item")
	}) {
		return true
	}

	if hasURL && containsAnyToken(tokens, func(token string) bool {
		return strings.Contains(token, "shellexecute") || strings.Contains(token, "shell.application")
	}) {
		return true
	}

	if len(tokens) > 0 {
		first := tokens[0]
		// Legacy ShellExecute path via url.dll.
		if first == "rundll32" &&
			containsAnyToken(tokens, func(token string) bool {
				return strings.Contains(token, "url.dll,fileprotocolhandler")
			}) && hasURL {
			return true
		}
		if first == "mshta" && hasURL {
			return true
		}
		if isBrowserExecutable(first) && hasURL {
			return true
		}
		if (first == "explorer" || first == "explorer.exe") && hasURL {
			return true
		}
	}

	// Check for force delete operations (e.g. Remove-Item -Force).
	return hasForceDeleteCmdlet(tokens)
}

func isDangerousCmd(command []string) bool {
	if len(command) == 0 {
		return false
	}
	base, ok := windowsExecutableBasename(command[0])
	if !ok || (base != "cmd" && base != "cmd.exe") {
		return false
	}

	index := 1
	for ; index < len(command); index++ {
		lower := strings.ToLower(command[index])
		switch lower {
		case "/c", "/r", "-c":
			index++
			goto body
		}
		if strings.HasPrefix(lower, "/") {
			continue
		}
		// Unknown tokens before the command body => bail.
		return false
	}

body:
	remaining := command[index:]
	if len(remaining) == 0 {
		return false
	}
	cmdTokens := remaining
	if len(remaining) == 1 {
		split, err := splitShellWords(remaining[0])
		if err != nil {
			split = []string{remaining[0]}
		}
		cmdTokens = split
	}
	var tokens []string
	for _, token := range cmdTokens {
		tokens = append(tokens, splitEmbeddedCmdOperators(token)...)
	}

	for _, segment := range splitCmdSegments(tokens) {
		if len(segment) == 0 {
			continue
		}
		cmd := segment[0]
		// Classic `cmd /c ... start https://...` ShellExecute path.
		if strings.EqualFold(cmd, "start") && argsHaveURL(segment) {
			return true
		}
		// Force delete: del /f, erase /f.
		if (strings.EqualFold(cmd, "del") || strings.EqualFold(cmd, "erase")) && hasForceFlagCmd(segment) {
			return true
		}
		// Recursive directory removal: rd /s /q, rmdir /s /q.
		if (strings.EqualFold(cmd, "rd") || strings.EqualFold(cmd, "rmdir")) &&
			hasRecursiveFlagCmd(segment) && hasQuietFlagCmd(segment) {
			return true
		}
	}
	return false
}

func isDirectGUILaunch(command []string) bool {
	if len(command) == 0 {
		return false
	}
	base, ok := windowsExecutableBasename(command[0])
	if !ok {
		return false
	}
	rest := command[1:]

	// Explorer/rundll32/mshta or direct browser exe with a URL anywhere in args.
	if (base == "explorer" || base == "explorer.exe") && argsHaveURL(rest) {
		return true
	}
	if (base == "mshta" || base == "mshta.exe") && argsHaveURL(rest) {
		return true
	}
	if (base == "rundll32" || base == "rundll32.exe") &&
		containsAnyToken(rest, func(token string) bool {
			return strings.Contains(strings.ToLower(token), "url.dll,fileprotocolhandler")
		}) && argsHaveURL(rest) {
		return true
	}
	if isBrowserExecutable(base) && argsHaveURL(rest) {
		return true
	}
	return false
}

func splitEmbeddedCmdOperators(token string) []string {
	// Split concatenated CMD operators so `echo hi&del` becomes
	// ["echo hi", "&", "del"]. Handles `&`, `&&`, `|`, `||`.
	var parts []string
	start := 0
	index := 0
	for index < len(token) {
		character := token[index]
		if character != '&' && character != '|' {
			index++
			continue
		}
		if index > start {
			parts = append(parts, token[start:index])
		}
		length := 1
		if index+1 < len(token) && token[index+1] == character {
			length = 2
		}
		parts = append(parts, token[index:index+length])
		index += length
		start = index
	}
	if start < len(token) {
		parts = append(parts, token[start:])
	}
	out := parts[:0]
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitCmdSegments(tokens []string) [][]string {
	var segments [][]string
	current := []string{}
	for _, token := range tokens {
		switch token {
		case "&", "&&", "|", "||":
			segments = append(segments, current)
			current = []string{}
		default:
			current = append(current, token)
		}
	}
	segments = append(segments, current)
	return segments
}

func hasForceDeleteCmdlet(tokens []string) bool {
	deleteCmdlets := map[string]bool{
		"remove-item": true, "ri": true, "rm": true,
		"del": true, "erase": true, "rd": true, "rmdir": true,
	}

	// Hard separators end a command segment, so -Force must be in the same one.
	const segmentSeparators = ";|&\n\r\t"
	// Soft separators can stick to tokens (blocks, parens, brackets, commas).
	const softSeparators = "{}()[],;"

	segments := [][]string{{}}
	for _, token := range tokens {
		var current strings.Builder
		for _, character := range token {
			if strings.ContainsRune(segmentSeparators, character) {
				text := strings.TrimSpace(current.String())
				if text != "" {
					last := len(segments) - 1
					segments[last] = append(segments[last], text)
				}
				current.Reset()
				if last := segments[len(segments)-1]; len(last) > 0 {
					segments = append(segments, []string{})
				}
				continue
			}
			current.WriteRune(character)
		}
		text := strings.TrimSpace(current.String())
		if text != "" {
			last := len(segments) - 1
			segments[last] = append(segments[last], text)
		}
	}

	for _, segment := range segments {
		hasDelete := false
		hasForce := false
		for _, token := range segment {
			for _, atom := range strings.FieldsFunc(token, func(character rune) bool {
				return strings.ContainsRune(softSeparators, character)
			}) {
				atom = strings.TrimSpace(atom)
				if atom == "" {
					continue
				}
				if deleteCmdlets[strings.ToLower(atom)] {
					hasDelete = true
				}
				if strings.EqualFold(atom, "-force") ||
					(len(atom) >= 7 && strings.EqualFold(atom[:7], "-force:")) {
					hasForce = true
				}
			}
		}
		if hasDelete && hasForce {
			return true
		}
	}
	return false
}

// hasForceFlagCmd reports a /f or /F flag in CMD del/erase arguments.
func hasForceFlagCmd(args []string) bool {
	return containsFlag(args, "/f")
}

// hasRecursiveFlagCmd reports a /s or /S flag in CMD rd/rmdir arguments.
func hasRecursiveFlagCmd(args []string) bool {
	return containsFlag(args, "/s")
}

// hasQuietFlagCmd reports a /q or /Q flag in CMD rd/rmdir arguments.
func hasQuietFlagCmd(args []string) bool {
	return containsFlag(args, "/q")
}

func containsFlag(args []string, flag string) bool {
	for _, arg := range args {
		if strings.EqualFold(arg, flag) {
			return true
		}
	}
	return false
}

func argsHaveURL(args []string) bool {
	for _, arg := range args {
		if looksLikeURL(arg) {
			return true
		}
	}
	return false
}

var urlCandidatePattern = regexp.MustCompile(`^[ "'\(\s]*([^\s"'\);]+)[\s;\)]*$`)

func looksLikeURL(token string) bool {
	// If the token embeds a URL alongside other text (for example
	// Start-Process('https://...')) as a single token, grab the substring
	// starting at the first URL prefix.
	lowercase := strings.ToLower(token)
	candidateToken := token
	if index := strings.Index(lowercase, "https://"); index >= 0 {
		candidateToken = token[index:]
	} else if index := strings.Index(lowercase, "http://"); index >= 0 {
		candidateToken = token[index:]
	}

	candidate := candidateToken
	if match := urlCandidatePattern.FindStringSubmatch(candidateToken); match != nil {
		candidate = match[1]
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func windowsExecutableBasename(executable string) (string, bool) {
	name := executable
	if index := strings.LastIndexAny(name, `/\`); index >= 0 {
		name = name[index+1:]
	}
	if name == "" {
		return "", false
	}
	if len(name) >= 2 && name[1] == ':' && isASCIIAlpha(name[0]) {
		name = name[2:]
	}
	if name == "" {
		return "", false
	}
	return strings.ToLower(name), true
}

func isPowerShellExecutable(executable string) bool {
	base, ok := windowsExecutableBasename(executable)
	if !ok {
		return false
	}
	switch base {
	case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return true
	}
	return false
}

func isBrowserExecutable(name string) bool {
	switch name {
	case "chrome", "chrome.exe", "msedge", "msedge.exe",
		"firefox", "firefox.exe", "iexplore", "iexplore.exe":
		return true
	}
	return false
}

// parsePowerShellInvocation parses a PowerShell invocation into a flat token
// list for the dangerous-word scan. This is a best-effort shlex split of the
// script text, not a full PowerShell parser.
func parsePowerShellInvocation(args []string) ([]string, bool) {
	if len(args) == 0 {
		return nil, false
	}
	index := 0
	for index < len(args) {
		argument := args[index]
		lower := strings.ToLower(argument)
		switch lower {
		case "-command", "/command", "-c":
			if index+1 >= len(args) || index+2 != len(args) {
				return nil, false
			}
			return splitShellWordsStrict(args[index+1])
		case "-nologo", "-noprofile", "-noninteractive", "-mta", "-sta":
			index++
			continue
		}
		if strings.HasPrefix(lower, "-command:") || strings.HasPrefix(lower, "/command:") {
			if index+1 != len(args) {
				return nil, false
			}
			_, script, found := strings.Cut(argument, ":")
			if !found {
				return nil, false
			}
			return splitShellWordsStrict(script)
		}
		if strings.HasPrefix(lower, "-") {
			index++
			continue
		}
		return append([]string(nil), args[index:]...), true
	}
	return nil, false
}

func containsAnyToken(tokens []string, predicate func(string) bool) bool {
	for _, token := range tokens {
		if predicate(token) {
			return true
		}
	}
	return false
}

func splitShellWordsStrict(input string) ([]string, bool) {
	words, err := splitShellWords(input)
	if err != nil {
		return nil, false
	}
	return words, true
}
