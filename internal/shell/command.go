package shell

import (
	"path/filepath"
	"runtime"
	"strings"
)

type ShellType string

const (
	ShellZsh        ShellType = "zsh"
	ShellBash       ShellType = "bash"
	ShellFish       ShellType = "fish"
	ShellPowerShell ShellType = "powershell"
	ShellSh         ShellType = "sh"
	ShellCmd        ShellType = "cmd"
)

type DetectedShell struct {
	ShellType ShellType `json:"shellType"`
	ShellPath string    `json:"shellPath"`
}

func (s *DetectedShell) Name() string {
	if s == nil {
		return ""
	}
	return string(s.ShellType)
}

func DetectShellType(shellPath string) (ShellType, bool) {
	name := strings.ToLower(filepath.Base(shellPath))
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "zsh":
		return ShellZsh, true
	case "bash":
		return ShellBash, true
	case "fish":
		return ShellFish, true
	case "pwsh", "powershell":
		return ShellPowerShell, true
	case "sh":
		return ShellSh, true
	case "cmd":
		return ShellCmd, true
	default:
		return "", false
	}
}

func UltimateFallbackShell() *DetectedShell {
	if runtime.GOOS == "windows" {
		return &DetectedShell{ShellType: ShellCmd, ShellPath: "cmd.exe"}
	}
	return &DetectedShell{ShellType: ShellSh, ShellPath: "/bin/sh"}
}

func GetShellByModelProvidedPath(shellPath string) *DetectedShell {
	shellType, ok := DetectShellType(shellPath)
	if !ok {
		return UltimateFallbackShell()
	}
	return &DetectedShell{ShellType: shellType, ShellPath: shellPath}
}

func ShlexJoin(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(tokens))
	for _, token := range tokens {
		quoted = append(quoted, shellQuote(token))
	}
	return strings.Join(quoted, " ")
}

func ExtractShellCommand(command []string) (string, string, bool) {
	if len(command) != 3 {
		return "", "", false
	}
	shellType, ok := DetectShellType(command[0])
	if !ok {
		return "", "", false
	}
	flag := strings.ToLower(command[1])
	switch shellType {
	case ShellBash, ShellZsh, ShellFish, ShellSh:
		if flag == "-lc" || flag == "-c" {
			return command[0], command[2], true
		}
	case ShellPowerShell:
		if flag == "-command" || flag == "-c" {
			return command[0], command[2], true
		}
	case ShellCmd:
		if flag == "/c" {
			return command[0], command[2], true
		}
	}
	return "", "", false
}

func ExtractPOSIXShellCommand(command []string) (string, string, bool) {
	if len(command) != 3 {
		return "", "", false
	}
	shellType, ok := DetectShellType(command[0])
	if !ok {
		return "", "", false
	}
	switch shellType {
	case ShellBash, ShellZsh, ShellSh:
	default:
		return "", "", false
	}
	if command[1] != "-lc" && command[1] != "-c" {
		return "", "", false
	}
	return command[0], command[2], true
}

const UTF8OutputPrefix = "try { [Console]::OutputEncoding=[System.Text.Encoding]::UTF8 } catch {}\n"

func ExtractPowerShellCommand(command []string) (string, string, bool) {
	if len(command) < 3 {
		return "", "", false
	}
	shellType, ok := DetectShellType(command[0])
	if !ok || shellType != ShellPowerShell {
		return "", "", false
	}
	for i := 1; i+1 < len(command); i++ {
		flag := strings.ToLower(command[i])
		switch flag {
		case "-nologo", "-noprofile":
			continue
		case "-command", "-c":
			return command[0], command[i+1], true
		default:
			return "", "", false
		}
	}
	return "", "", false
}

func PrefixPowerShellScriptWithUTF8(command []string) []string {
	_, script, ok := ExtractPowerShellCommand(command)
	if !ok {
		return append([]string(nil), command...)
	}
	prefixed := strings.TrimLeft(script, " \t\r\n")
	if !strings.HasPrefix(prefixed, UTF8OutputPrefix) {
		script = UTF8OutputPrefix + script
	}
	result := append([]string(nil), command[:len(command)-1]...)
	result = append(result, script)
	return result
}

func StripShellCommandAndEscape(command []string) string {
	if len(command) == 0 {
		return ""
	}
	if _, script, ok := ExtractPOSIXShellCommand(command); ok {
		return script
	}
	if _, script, ok := ExtractPowerShellCommand(command); ok {
		return script
	}
	return ShlexJoin(command)
}

type ParsedCommand struct {
	Kind string   `json:"kind"`
	Args []string `json:"args,omitempty"`
}

func ParseCommand(command []string) []ParsedCommand {
	if len(command) == 0 {
		return nil
	}
	if _, script, ok := ExtractShellCommand(command); ok {
		return []ParsedCommand{{Kind: "shell", Args: []string{script}}}
	}
	if _, script, ok := ExtractPowerShellCommand(command); ok {
		return []ParsedCommand{{Kind: "powershell", Args: []string{script}}}
	}
	return []ParsedCommand{{Kind: "unknown", Args: append([]string(nil), command...)}}
}

// SplitCommandLine mirrors the shlex split used by Rust for model-provided
// shell command strings. Malformed quoting falls back to whitespace splitting.
func SplitCommandLine(command string) []string {
	tokens, ok := splitCommandLine(command)
	if !ok {
		return strings.Fields(command)
	}
	return tokens
}

// ReadPaths returns file operands from the read commands recognized by the
// implicit skill invocation fixtures. Connector-separated commands are
// inspected independently and a leading cd is applied to later relative paths.
func ReadPaths(command []string) []string {
	command = normalizeReadCommand(command)
	parts := splitCommandParts(command)
	paths := make([]string, 0, len(parts))
	var cwd string
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		if part[0] == "cd" {
			if target := lastNonFlagOperand(part[1:], nil); target != "" {
				if cwd == "" || filepath.IsAbs(target) {
					cwd = target
				} else {
					cwd = filepath.Join(cwd, target)
				}
			}
			continue
		}
		path := readPath(part)
		if path == "" {
			continue
		}
		if cwd != "" && !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		paths = append(paths, path)
	}
	return paths
}

func normalizeReadCommand(command []string) []string {
	if len(command) >= 3 {
		shell := strings.ToLower(filepath.Base(command[0]))
		shell = strings.TrimSuffix(shell, ".exe")
		if (shell == "bash" || shell == "zsh") && (command[1] == "-c" || command[1] == "-lc") {
			return SplitCommandLine(command[2])
		}
	}
	if len(command) >= 3 && (command[0] == "yes" || command[0] == "y" || command[0] == "no" || command[0] == "n") && command[1] == "|" {
		return command[2:]
	}
	return command
}

func splitCommandLine(command string) ([]string, bool) {
	var tokens []string
	var current strings.Builder
	var quote rune
	escaped := false
	started := false
	flush := func() {
		if !started {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
		started = false
	}
	for _, r := range command {
		switch {
		case escaped:
			current.WriteRune(r)
			started = true
			escaped = false
		case quote == '\'' && r == '\'':
			quote = 0
		case quote == '\'':
			current.WriteRune(r)
			started = true
		case r == '\\':
			escaped = true
			started = true
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			current.WriteRune(r)
			started = true
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			flush()
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	return tokens, true
}

func splitCommandParts(command []string) [][]string {
	parts := make([][]string, 0, 1)
	current := make([]string, 0, len(command))
	for _, token := range command {
		switch token {
		case "&&", "||", "|", ";":
			if len(current) > 0 {
				parts = append(parts, current)
				current = nil
			}
		default:
			current = append(current, token)
		}
	}
	if len(current) > 0 {
		parts = append(parts, current)
	}
	return parts
}

func readPath(command []string) string {
	if len(command) == 0 {
		return ""
	}
	switch command[0] {
	case "cat":
		return singleNonFlagOperand(command[1:], nil)
	case "bat", "batcat":
		return singleNonFlagOperand(command[1:], stringSet(
			"--theme", "--language", "--style", "--terminal-width", "--tabs", "--line-range", "--map-syntax",
		))
	case "less":
		return singleNonFlagOperand(command[1:], stringSet(
			"-p", "-P", "-x", "-y", "-z", "-j", "--pattern", "--prompt", "--tabs", "--shift", "--jump-target",
		))
	case "more":
		return singleNonFlagOperand(command[1:], nil)
	case "head":
		return headReadPath(command[1:])
	case "tail":
		return tailReadPath(command[1:])
	case "awk":
		return awkReadPath(command[1:])
	case "nl":
		return firstNonFlagOperand(command[1:], stringSet("-s", "-w", "-v", "-i", "-b"))
	case "sed":
		return sedReadPath(command[1:])
	default:
		return ""
	}
}

func headReadPath(args []string) string {
	if len(args) == 1 && !strings.HasPrefix(args[0], "-") {
		return args[0]
	}
	if len(args) == 0 {
		return ""
	}
	start := 1
	valid := false
	if args[0] == "-n" && len(args) > 1 {
		valid = isDecimal(args[1], false)
		start = 2
	} else if strings.HasPrefix(args[0], "-n") {
		valid = isDecimal(strings.TrimPrefix(args[0], "-n"), false)
	}
	if !valid {
		return ""
	}
	return firstNonFlagOperand(args[start:], nil)
}

func tailReadPath(args []string) string {
	if len(args) == 1 && !strings.HasPrefix(args[0], "-") {
		return args[0]
	}
	if len(args) == 0 {
		return ""
	}
	start := 1
	valid := false
	if args[0] == "-n" && len(args) > 1 {
		valid = isDecimal(args[1], true)
		start = 2
	} else if strings.HasPrefix(args[0], "-n") {
		valid = isDecimal(strings.TrimPrefix(args[0], "-n"), true)
	}
	if !valid {
		return ""
	}
	return firstNonFlagOperand(args[start:], nil)
}

func isDecimal(value string, allowPlus bool) bool {
	if allowPlus {
		value = strings.TrimPrefix(value, "+")
	}
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func awkReadPath(args []string) string {
	hasScriptFile := false
	for _, arg := range args {
		if arg == "-f" || arg == "--file" {
			hasScriptFile = true
			break
		}
	}
	operands := nonFlagOperands(args, stringSet("-F", "-v", "-f", "--field-separator", "--assign", "--file"))
	if hasScriptFile {
		if len(operands) > 0 {
			return operands[0]
		}
		return ""
	}
	if len(operands) >= 2 {
		return operands[1]
	}
	return ""
}

func sedReadPath(args []string) string {
	hasPrintOnly := false
	for _, arg := range args {
		if arg == "-n" {
			hasPrintOnly = true
			break
		}
	}
	if !hasPrintOnly {
		return ""
	}
	hasRange := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "-e" || arg == "--expression" {
			if index+1 < len(args) && isSedPrintRange(args[index+1]) {
				hasRange = true
			}
			index++
			continue
		}
		if arg == "-f" || arg == "--file" {
			index++
			continue
		}
		if !strings.HasPrefix(arg, "-") && isSedPrintRange(arg) {
			hasRange = true
		}
	}
	if !hasRange {
		return ""
	}
	operands := nonFlagOperands(args, stringSet("-e", "-f", "--expression", "--file"))
	if len(operands) == 0 {
		return ""
	}
	if isSedPrintRange(operands[0]) {
		if len(operands) > 1 {
			return operands[1]
		}
		return ""
	}
	return operands[0]
}

func isSedPrintRange(value string) bool {
	if !strings.HasSuffix(value, "p") {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(value, "p"), ",")
	if len(parts) < 1 || len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if !isDecimal(part, false) {
			return false
		}
	}
	return true
}

func singleNonFlagOperand(args []string, flagsWithValues map[string]bool) string {
	operands := nonFlagOperands(args, flagsWithValues)
	if len(operands) != 1 {
		return ""
	}
	return operands[0]
}

func firstNonFlagOperand(args []string, flagsWithValues map[string]bool) string {
	operands := nonFlagOperands(args, flagsWithValues)
	if len(operands) == 0 {
		return ""
	}
	return operands[0]
}

func lastNonFlagOperand(args []string, flagsWithValues map[string]bool) string {
	operands := nonFlagOperands(args, flagsWithValues)
	if len(operands) == 0 {
		return ""
	}
	return operands[len(operands)-1]
}

func nonFlagOperands(args []string, flagsWithValues map[string]bool) []string {
	operands := make([]string, 0, len(args))
	afterDoubleDash := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if afterDoubleDash {
			operands = append(operands, arg)
			continue
		}
		if arg == "--" {
			afterDoubleDash = true
			continue
		}
		if strings.HasPrefix(arg, "--") && strings.Contains(arg, "=") {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if flagsWithValues[arg] && index+1 < len(args) {
				index++
			}
			continue
		}
		operands = append(operands, arg)
	}
	return operands
}

func stringSet(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func shellQuote(token string) string {
	if token == "" {
		return "''"
	}
	if !strings.ContainsAny(token, " \t\r\n'\"\\$`!&|;<>*?()[]{}") {
		return token
	}
	return "'" + strings.ReplaceAll(token, "'", "'\\''") + "'"
}
