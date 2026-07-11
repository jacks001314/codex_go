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

func shellQuote(token string) string {
	if token == "" {
		return "''"
	}
	if !strings.ContainsAny(token, " \t\r\n'\"\\$`!&|;<>*?()[]{}") {
		return token
	}
	return "'" + strings.ReplaceAll(token, "'", "'\\''") + "'"
}
