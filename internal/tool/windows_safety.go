package tool

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	powerShellPipelineToCmdDestructivePattern = regexp.MustCompile(`(?i)\|\s*(?:cmd(?:\.exe)?\s*/c|cmd\s+/c)\b.*\b(?:del|erase|rmdir|rd|move|ren|rename)\b`)
	powerShellStartProcessPattern             = regexp.MustCompile(`(?i)\bStart-Process\b`)
	powerShellRecursiveDeleteMovePattern      = regexp.MustCompile(`(?i)\b(?:Remove-Item|Move-Item)\b[\s\S]*\-(?:Recurse|r)\b`)
	powerShellPathArgPattern                  = regexp.MustCompile(`(?i)\-(LiteralPath|Path)\s+('[^']*'|"[^"]*"|[^\s|;&]+)`)
)

func validateWindowsShellSafety(command string, shellType ShellType, cwd string) error {
	switch shellType {
	case ShellPowerShell:
		return validatePowerShellSafety(command, cwd)
	case ShellCmd:
		return validateCmdSafety(command)
	default:
		return nil
	}
}

func validatePowerShellSafety(command string, cwd string) error {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return nil
	}
	if powerShellPipelineToCmdDestructivePattern.MatchString(trimmed) {
		return fmt.Errorf("blocked unsafe Windows command: do not pipe PowerShell enumeration into cmd /c for deletion or moving; keep the operation in PowerShell with explicit LiteralPath targets")
	}
	if powerShellStartProcessPattern.MatchString(trimmed) && !hasPowerShellFlag(trimmed, "WindowStyle") {
		return fmt.Errorf("blocked unsafe Windows command: Start-Process must include -WindowStyle Hidden unless a visible interactive window is explicitly required")
	}
	if powerShellRecursiveDeleteMovePattern.MatchString(trimmed) {
		if !hasPowerShellFlag(trimmed, "LiteralPath") {
			return fmt.Errorf("blocked unsafe Windows command: recursive Remove-Item/Move-Item must use -LiteralPath with explicit resolved targets")
		}
		if !powerShellLiteralPathsStayWithinCWD(trimmed, cwd) {
			return fmt.Errorf("blocked unsafe Windows command: recursive Remove-Item/Move-Item targets must resolve within the current workspace")
		}
	}
	return nil
}

func validateCmdSafety(command string) error {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return nil
	}
	if strings.Contains(strings.ToLower(trimmed), "powershell") && containsCmdDestructiveOperation(trimmed) {
		return fmt.Errorf("blocked unsafe Windows command: do not compose PowerShell enumeration with cmd /c destructive file operations")
	}
	return nil
}

func hasPowerShellFlag(command string, name string) bool {
	flag := "-" + strings.ToLower(name)
	for _, token := range powerShellTokens(command) {
		if strings.EqualFold(token, flag) {
			return true
		}
	}
	return false
}

func powerShellLiteralPathsStayWithinCWD(command string, cwd string) bool {
	base := strings.TrimSpace(cwd)
	if base == "" {
		return false
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	baseAbs = filepath.Clean(baseAbs)
	foundLiteralPath := false
	for _, match := range powerShellPathArgPattern.FindAllStringSubmatch(command, -1) {
		if len(match) < 3 || !strings.EqualFold(match[1], "LiteralPath") {
			if len(match) >= 2 && strings.EqualFold(match[1], "Path") {
				return false
			}
			continue
		}
		foundLiteralPath = true
		target := unquoteShellToken(match[2])
		if strings.ContainsAny(target, "*?") {
			return false
		}
		if !pathStaysWithinBase(target, baseAbs) {
			return false
		}
	}
	return foundLiteralPath
}

func pathStaysWithinBase(target string, baseAbs string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(baseAbs, target)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	targetAbs = filepath.Clean(targetAbs)
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func containsCmdDestructiveOperation(command string) bool {
	lower := strings.ToLower(command)
	return containsShellWord(lower, "del") ||
		containsShellWord(lower, "erase") ||
		containsShellWord(lower, "rmdir") ||
		containsShellWord(lower, "rd") ||
		containsShellWord(lower, "move") ||
		containsShellWord(lower, "ren") ||
		containsShellWord(lower, "rename")
}

func containsShellWord(command string, word string) bool {
	pattern := regexp.MustCompile(`(?i)(^|[^a-z0-9_])` + regexp.QuoteMeta(word) + `([^a-z0-9_]|$)`)
	return pattern.MatchString(command)
}

func powerShellTokens(command string) []string {
	var tokens []string
	var current strings.Builder
	var quote rune
	escaped := false
	for _, ch := range command {
		if escaped {
			current.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '`' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
				continue
			}
			current.WriteRune(ch)
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
		case ' ', '\t', '\r', '\n', '|', ';', '&':
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func unquoteShellToken(token string) string {
	token = strings.TrimSpace(token)
	if len(token) >= 2 {
		if token[0] == '\'' && token[len(token)-1] == '\'' || token[0] == '"' && token[len(token)-1] == '"' {
			return token[1 : len(token)-1]
		}
	}
	return token
}
