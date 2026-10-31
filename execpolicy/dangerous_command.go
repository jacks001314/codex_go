package execpolicy

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// DangerousCommandMatch identifies the dangerous-command rule matched by a
// command invocation (Rust `DangerousCommandMatch`, codex-shell-command).
type DangerousCommandMatch string

const (
	// DangerousCommandMatchForcedRm means an `rm` invocation includes the force
	// option.
	DangerousCommandMatchForcedRm DangerousCommandMatch = "forced_rm"
	// DangerousCommandMatchOther means another dangerous-command rule matched.
	DangerousCommandMatchOther DangerousCommandMatch = "other"
)

// MaxDangerousCommandWrapperDepth bounds wrapper unwrapping (`sudo`, `env`,
// `trap`, nested shell scripts) so a pathological command graph fails closed.
const MaxDangerousCommandWrapperDepth = 8

// MatchDangerousCommand returns the dangerous-command rule matched by an
// already-tokenized command using the host platform's semantics.
func MatchDangerousCommand(command []string) (DangerousCommandMatch, bool) {
	return MatchDangerousCommandForPlatform(command, HostDangerousCommandPlatform())
}

// MatchDangerousCommandForPlatform returns the dangerous-command rule matched
// using the target platform's command and path semantics (Rust #42113).
func MatchDangerousCommandForPlatform(command []string, platform DangerousCommandPlatform) (DangerousCommandMatch, bool) {
	return dangerousCommandMatchWithDepth(command, 0, normalizedDangerousCommandPlatform(platform))
}

// MatchDangerousPowerShellWordsForPlatform matches already-tokenized words
// taken from a top-level PowerShell `-Command` body. PowerShell rules only
// apply to Windows semantics.
func MatchDangerousPowerShellWordsForPlatform(words []string, platform DangerousCommandPlatform) (DangerousCommandMatch, bool) {
	if normalizedDangerousCommandPlatform(platform) != DangerousCommandPlatformWindows {
		return "", false
	}
	if isDangerousPowerShellWords(words) {
		return DangerousCommandMatchOther, true
	}
	return "", false
}

// DangerousCommandRejectionReason is the model-facing reason for a matched
// dangerous-command rule (Rust `dangerous_command_rejection_reason`).
func DangerousCommandRejectionReason(match DangerousCommandMatch) string {
	switch match {
	case DangerousCommandMatchForcedRm:
		return "rm -f style commands are not permitted. Use a safer approach"
	default:
		return "blocked by policy"
	}
}

// DangerousCommandPlatformForExecutorOS maps an executor-reported OS to the
// command semantics used for safety checks (Rust #42113 process_manager). A
// legacy executor that reports no platform keeps host semantics; any other
// reported platform uses Windows rules conservatively.
func DangerousCommandPlatformForExecutorOS(platformOS string) DangerousCommandPlatform {
	switch strings.ToLower(strings.TrimSpace(platformOS)) {
	case "linux", "macos":
		return DangerousCommandPlatformPosix
	case "":
		return HostDangerousCommandPlatform()
	default:
		return DangerousCommandPlatformWindows
	}
}

func normalizedDangerousCommandPlatform(platform DangerousCommandPlatform) DangerousCommandPlatform {
	switch platform {
	case DangerousCommandPlatformWindows, DangerousCommandPlatformPosix:
		return platform
	default:
		return HostDangerousCommandPlatform()
	}
}

func dangerousCommandMatchWithDepth(command []string, wrapperDepth int, platform DangerousCommandPlatform) (DangerousCommandMatch, bool) {
	if wrapperDepth > MaxDangerousCommandWrapperDepth {
		return DangerousCommandMatchOther, true
	}

	if match, ok := dangerousCommandMatchForExec(command, wrapperDepth, platform); ok {
		return match, true
	}

	// Support shell scripts where any literal command might be dangerous,
	// including commands nested in control flow or substitutions.
	if commands, ok := parseShellLCLiteralCommands(command); ok {
		for _, nested := range commands {
			if match, ok := dangerousCommandMatchWithDepth(nested, wrapperDepth+1, platform); ok {
				return match, true
			}
		}
	}

	if platform == DangerousCommandPlatformWindows && isDangerousCommandWindows(command) {
		return DangerousCommandMatchOther, true
	}

	return "", false
}

func dangerousCommandMatchForExec(command []string, wrapperDepth int, platform DangerousCommandPlatform) (DangerousCommandMatch, bool) {
	if len(command) == 0 {
		return "", false
	}
	executable, ok := dangerousExecutableNameLookupKey(command[0], platform)
	if !ok {
		return "", false
	}

	switch executable {
	case "rm":
		if rmArgsIncludeForceOption(command[1:]) {
			return DangerousCommandMatchForcedRm, true
		}
	case "sudo":
		// For sudo <cmd>, simply check <cmd>.
		return dangerousCommandMatchWithDepth(command[1:], wrapperDepth+1, platform)
	case "env":
		// Skip environment assignments before checking the command run by env.
		return dangerousCommandMatchForEnv(command, wrapperDepth, platform)
	case "trap":
		// A trap action is shell source stored in the first operand.
		return dangerousCommandMatchForTrap(command, wrapperDepth, platform)
	}
	return "", false
}

func dangerousCommandMatchForEnv(command []string, wrapperDepth int, platform DangerousCommandPlatform) (DangerousCommandMatch, bool) {
	commandIndex := 1
	for commandIndex < len(command) {
		argument := command[commandIndex]
		if argument == "--" {
			commandIndex++
			break
		}
		if argument == "-i" || argument == "--ignore-environment" {
			commandIndex++
			continue
		}
		if name, _, found := strings.Cut(argument, "="); found && name != "" && !strings.HasPrefix(name, "-") {
			commandIndex++
			continue
		}
		break
	}
	return dangerousCommandMatchWithDepth(command[commandIndex:], wrapperDepth+1, platform)
}

func dangerousCommandMatchForTrap(command []string, wrapperDepth int, platform DangerousCommandPlatform) (DangerousCommandMatch, bool) {
	actionIndex := 1
	if actionIndex < len(command) && command[actionIndex] == "--" {
		actionIndex++
	}
	if actionIndex >= len(command) {
		return "", false
	}
	action := command[actionIndex]
	if strings.HasPrefix(action, "-") {
		return "", false
	}
	shellCommand := []string{"sh", "-c", action}
	return dangerousCommandMatchWithDepth(shellCommand, wrapperDepth+1, platform)
}

func rmArgsIncludeForceOption(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--force" {
			return true
		}
		flags := strings.TrimPrefix(arg, "-")
		if flags != arg && !strings.HasPrefix(flags, "-") && strings.Contains(flags, "f") {
			return true
		}
	}
	return false
}

// dangerousExecutableNameLookupKey mirrors Rust shell-command's
// `executable_name_lookup_key`: POSIX keeps case and suffix, Windows strips the
// directory, drive prefix, executable suffix, and case.
func dangerousExecutableNameLookupKey(raw string, platform DangerousCommandPlatform) (string, bool) {
	if platform != DangerousCommandPlatformWindows {
		name := raw
		if index := strings.LastIndex(raw, "/"); index >= 0 {
			name = raw[index+1:]
		}
		if name == "" {
			return "", false
		}
		return name, true
	}
	name := raw
	if index := strings.LastIndexAny(name, `/\`); index >= 0 {
		name = name[index+1:]
	}
	if name == "" {
		return "", false
	}
	if len(name) >= 2 && name[1] == ':' && isASCIIAlpha(name[0]) {
		name = name[2:]
	}
	name = strings.ToLower(name)
	for _, suffix := range []string{".exe", ".cmd", ".bat", ".com"} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix), true
		}
	}
	if name == "" {
		return "", false
	}
	return name, true
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

// parseShellLCLiteralCommands extracts the statically known words of every
// command node in a `bash|zsh|sh -lc|-c <script>` invocation (Rust
// `parse_shell_lc_literal_commands`). Dynamic words and unparsable scripts are
// omitted, so this is only suitable for flagging dangerous literals, never for
// proving a command safe.
func parseShellLCLiteralCommands(command []string) ([][]string, bool) {
	if len(command) != 3 {
		return nil, false
	}
	if command[1] != "-lc" && command[1] != "-c" {
		return nil, false
	}
	switch dangerousShellType(command[0]) {
	case "bash", "zsh", "sh":
	default:
		return nil, false
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command[2]), "")
	if err != nil {
		return nil, false
	}
	var commands [][]string
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		// The command name must be statically known; dynamic arguments are
		// omitted instead of dropping the whole command (Rust
		// parse_literal_command_from_node).
		commandName, ok := literalWord(call.Args[0])
		if !ok {
			return true
		}
		words := make([]string, 0, len(call.Args))
		words = append(words, commandName)
		for _, argument := range call.Args[1:] {
			if word, ok := literalWord(argument); ok {
				words = append(words, word)
			}
		}
		commands = append(commands, words)
		return true
	})
	return commands, true
}

func literalWord(word *syntax.Word) (string, bool) {
	var builder strings.Builder
	for _, part := range word.Parts {
		switch value := part.(type) {
		case *syntax.Lit:
			if isExpansionSensitiveWord(value.Value) {
				return "", false
			}
			builder.WriteString(value.Value)
		case *syntax.SglQuoted:
			if value.Dollar {
				return "", false
			}
			builder.WriteString(value.Value)
		case *syntax.DblQuoted:
			if value.Dollar {
				return "", false
			}
			for _, inner := range value.Parts {
				literal, ok := inner.(*syntax.Lit)
				if !ok || hasDoubleQuoteEscape(literal.Value) {
					return "", false
				}
				builder.WriteString(literal.Value)
			}
		default:
			return "", false
		}
	}
	return builder.String(), true
}

// isExpansionSensitiveWord mirrors Rust's `is_literal_word_or_number`: an
// unquoted shell word with glob, brace, escape, substitution, or comment
// syntax cannot be used as proof of the runtime argv.
func isExpansionSensitiveWord(value string) bool {
	return strings.HasPrefix(value, "=") || strings.ContainsAny(value, "{}*?[]\\~^#$`")
}

// hasDoubleQuoteEscape mirrors Rust's refusal to accept double-quoted contents
// whose source spelling contains escapes the shell would remove.
func hasDoubleQuoteEscape(value string) bool {
	for index := 0; index+1 < len(value); index++ {
		if value[index] == '\\' && strings.ContainsRune("$`\"\\\n", rune(value[index+1])) {
			return true
		}
	}
	return false
}

func dangerousShellType(path string) string {
	base := filepath.Base(strings.ReplaceAll(path, `\`, "/"))
	base = strings.ToLower(base)
	base = strings.TrimSuffix(base, ".exe")
	return base
}
