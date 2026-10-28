package applypatch

import "strings"

// applyPatchCommandNames are the executable names exec intercepts (Rust
// `APPLY_PATCH_COMMANDS`).
var applyPatchCommandNames = []string{"apply_patch", "applypatch"}

// InterceptsCommand reports whether exec intercepts argv as an apply_patch
// invocation, without reading any patch target files (Rust #57573
// `MaybeApplyPatch::intercepts_command`). The recognized forms are the explicit
// two-argument invocation, a shell heredoc whose body is malformed or valid, and
// an implicitly passed raw patch body.
func InterceptsCommand(argv []string, cwd string) bool {
	if isImplicitPatchInvocation(argv, cwd) {
		return true
	}
	return isExplicitApplyPatchInvocation(argv, cwd)
}

func isImplicitPatchInvocation(argv []string, cwd string) bool {
	var body string
	switch {
	case len(argv) == 1:
		body = argv[0]
	default:
		script, ok := parseApplyPatchShellScript(argv, cwd)
		if !ok {
			return false
		}
		body = script
	}
	_, err := Parse(body)
	return err == nil
}

func isExplicitApplyPatchInvocation(argv []string, cwd string) bool {
	// `apply_patch <body>`: even a malformed body is an intercepted invocation,
	// because exec owns the tool's error surface.
	if len(argv) == 2 && isApplyPatchCommandName(argv[0]) {
		return true
	}
	script, ok := parseApplyPatchShellScript(argv, cwd)
	if !ok {
		return false
	}
	_, ok = extractApplyPatchHeredocBody(script)
	return ok
}

func isApplyPatchCommandName(name string) bool {
	for _, candidate := range applyPatchCommandNames {
		if name == candidate {
			return true
		}
	}
	return false
}

type applyPatchShellKind int

const (
	applyPatchShellNone applyPatchShellKind = iota
	applyPatchShellUnix
	applyPatchShellPowerShell
	applyPatchShellCmd
)

// parseApplyPatchShellScript unwraps the shell forms Rust's `parse_shell_script`
// accepts: `bash|zsh|sh -c|-lc <script>`, `pwsh|powershell [-NoProfile]
// -Command <script>`, and `cmd /c <script>`.
func parseApplyPatchShellScript(argv []string, _ string) (string, bool) {
	switch len(argv) {
	case 3:
		if classifyApplyPatchShell(argv[0], argv[1]) == applyPatchShellNone {
			return "", false
		}
		return argv[2], true
	case 4:
		if !shellSupportsNoProfileFlag(argv[0]) || !strings.EqualFold(argv[1], "-noprofile") {
			return "", false
		}
		if classifyApplyPatchShell(argv[0], argv[2]) == applyPatchShellNone {
			return "", false
		}
		return argv[3], true
	default:
		return "", false
	}
}

func classifyApplyPatchShell(shell string, flag string) applyPatchShellKind {
	switch name := shellBasename(shell); {
	case (name == "bash" || name == "zsh" || name == "sh") && (flag == "-lc" || flag == "-c"):
		return applyPatchShellUnix
	case (name == "pwsh" || name == "powershell") && strings.EqualFold(flag, "-command"):
		return applyPatchShellPowerShell
	case name == "cmd" && strings.EqualFold(flag, "/c"):
		return applyPatchShellCmd
	default:
		return applyPatchShellNone
	}
}

func shellSupportsNoProfileFlag(shell string) bool {
	name := shellBasename(shell)
	return name == "pwsh" || name == "powershell"
}

// shellBasename mirrors Rust `classify_shell_name`: the final path segment with
// a non-empty extension stem, lowercased.
func shellBasename(shell string) string {
	base := strings.TrimSpace(shell)
	if index := strings.LastIndexAny(base, `/\\`); index >= 0 {
		base = base[index+1:]
	}
	if dot := strings.LastIndex(base, "."); dot > 0 {
		base = base[:dot]
	}
	return strings.ToLower(base)
}

// extractApplyPatchHeredocBody recognizes the two conservative forms Rust's
// tree-sitter query matches:
//
//	apply_patch <<'EOF'\n...\nEOF
//	cd <path> && apply_patch <<'EOF'\n...\nEOF
//
// The heredoc must be the only top-level statement; trailing commands make the
// script ineligible.
func extractApplyPatchHeredocBody(script string) (string, bool) {
	rest := strings.TrimLeft(script, " \t\r\n")
	if hasShellWordPrefix(rest, "cd") {
		afterCD := strings.TrimLeft(rest[len("cd"):], " \t")
		if _, remaining, ok := shellWord(afterCD); ok {
			remaining = strings.TrimLeft(remaining, " \t")
			if strings.HasPrefix(remaining, "&&") {
				rest = strings.TrimLeft(remaining[len("&&"):], " \t")
			}
		}
	}
	command, remaining, ok := shellWord(rest)
	if !ok || !isApplyPatchCommandName(command) {
		return "", false
	}
	remaining = strings.TrimLeft(remaining, " \t")
	if !strings.HasPrefix(remaining, "<<") {
		return "", false
	}
	stripTabs := strings.HasPrefix(remaining, "<<-")
	remaining = strings.TrimPrefix(remaining, "<<")
	remaining = strings.TrimPrefix(remaining, "-")
	remaining = strings.TrimLeft(remaining, " \t")
	delimiter, remaining, ok := heredocDelimiter(remaining)
	if !ok || delimiter == "" {
		return "", false
	}
	if !strings.HasPrefix(remaining, "\n") {
		return "", false
	}
	lines := strings.Split(remaining[1:], "\n")
	body := make([]string, 0, len(lines))
	for index, line := range lines {
		candidate := strings.TrimSuffix(line, "\r")
		comparison := candidate
		if stripTabs {
			comparison = strings.TrimLeft(comparison, "\t")
		}
		if comparison == delimiter {
			if strings.TrimSpace(strings.Join(lines[index+1:], "\n")) != "" {
				return "", false
			}
			return strings.Join(body, "\n"), true
		}
		body = append(body, candidate)
	}
	return "", false
}

func hasShellWordPrefix(input string, word string) bool {
	if !strings.HasPrefix(input, word) {
		return false
	}
	if len(input) == len(word) {
		return true
	}
	switch input[len(word)] {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

// shellWord reads one unquoted or quoted shell word and returns it with the
// remainder of the input.
func shellWord(input string) (string, string, bool) {
	if input == "" {
		return "", "", false
	}
	switch input[0] {
	case '\'':
		if end := strings.IndexByte(input[1:], '\''); end >= 0 {
			return input[1 : 1+end], input[1+end+1:], true
		}
		return "", "", false
	case '"':
		if end := strings.IndexByte(input[1:], '"'); end >= 0 {
			return input[1 : 1+end], input[1+end+1:], true
		}
		return "", "", false
	}
	end := strings.IndexAny(input, " \t\r\n;&|<>")
	if end < 0 {
		return input, "", true
	}
	if end == 0 {
		return "", "", false
	}
	return input[:end], input[end:], true
}

// heredocDelimiter reads the delimiter word, accepting the quoted forms bash
// allows (`'EOF'`, `"EOF"`) and backslash escaping.
func heredocDelimiter(input string) (string, string, bool) {
	word, remaining, ok := shellWord(input)
	if !ok {
		return "", "", false
	}
	word = strings.TrimPrefix(word, `\`)
	return word, remaining, true
}
