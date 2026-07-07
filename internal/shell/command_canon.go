package shell

import (
	"path/filepath"
	"strings"
)

const (
	CanonicalBashScriptPrefix       = "__codex_shell_script__"
	CanonicalPowerShellScriptPrefix = "__codex_powershell_script__"
)

func CanonicalizeForApproval(command []string) []string {
	if plain := parseShellLCPlainCommand(command); len(plain) > 0 {
		return plain
	}
	if shell, mode, script, ok := extractBash(command); ok {
		_ = shell
		return []string{CanonicalBashScriptPrefix, mode, script}
	}
	if _, script, ok := extractPowerShell(command); ok {
		return []string{CanonicalPowerShellScriptPrefix, script}
	}
	return append([]string(nil), command...)
}

func parseShellLCPlainCommand(command []string) []string {
	_, _, script, ok := extractBash(command)
	if !ok {
		return nil
	}
	tokens := splitPlainShell(script)
	if len(tokens) == 0 || containsShellSyntax(tokens) {
		return nil
	}
	return tokens
}

func extractBash(command []string) (string, string, string, bool) {
	if len(command) < 3 {
		return "", "", "", false
	}
	shell := baseName(command[0])
	if shell != "bash" && shell != "sh" && shell != "zsh" {
		return "", "", "", false
	}
	mode := command[1]
	if mode != "-lc" && mode != "-c" {
		return "", "", "", false
	}
	return shell, mode, command[2], true
}

func extractPowerShell(command []string) (string, string, bool) {
	if len(command) < 3 {
		return "", "", false
	}
	shell := strings.ToLower(baseName(command[0]))
	if shell != "powershell" && shell != "powershell.exe" && shell != "pwsh" && shell != "pwsh.exe" {
		return "", "", false
	}
	for i := 1; i < len(command)-1; i++ {
		flag := strings.ToLower(command[i])
		if flag == "-command" || flag == "-c" || flag == "/c" {
			return shell, command[i+1], true
		}
	}
	return "", "", false
}

func splitPlainShell(script string) []string {
	var tokens []string
	var current strings.Builder
	var quote rune
	escaped := false
	for _, r := range script {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case quote != 0:
			if r == '\\' && quote == '"' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '\\':
			escaped = true
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func containsShellSyntax(tokens []string) bool {
	for _, token := range tokens {
		if strings.ContainsAny(token, "|&;<>()$`") {
			return true
		}
		if strings.Contains(token, "&&") || strings.Contains(token, "||") {
			return true
		}
	}
	return false
}

func baseName(path string) string {
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) {
		return path
	}
	return strings.ToLower(base)
}
