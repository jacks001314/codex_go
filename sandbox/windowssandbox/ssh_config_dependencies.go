package windowssandbox

import (
	"os"
	"path/filepath"
	"strings"
)

var sshProfilePathDirectives = map[string]bool{
	"certificatefile":      true,
	"controlpath":          true,
	"globalknownhostsfile": true,
	"identityagent":        true,
	"identityfile":         true,
	"revokedhostkeys":      true,
	"userknownhostsfile":   true,
}

func SSHConfigDependencyPaths(home string) []string {
	if home == "" {
		return nil
	}
	sshDir := filepath.Join(home, ".ssh")
	config := filepath.Join(sshDir, "config")
	paths := []string{config}
	visitSSHConfig(config, home, sshDir, map[string]bool{}, &paths, 0)
	return paths
}

func visitSSHConfig(path string, userProfile string, sshDir string, visited map[string]bool, paths *[]string, depth int) {
	if depth == 32 {
		return
	}
	key := CanonicalPathKey(path)
	if visited[key] {
		return
	}
	visited[key] = true
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, args, ok := sshDirective(line)
		if !ok {
			continue
		}
		switch lower := strings.ToLower(key); {
		case lower == "include":
			for _, arg := range args {
				for _, include := range sshIncludePaths(arg, userProfile, sshDir) {
					*paths = append(*paths, include)
					visitSSHConfig(include, userProfile, sshDir, visited, paths, depth+1)
				}
			}
		case sshProfilePathDirectives[lower]:
			for _, arg := range args {
				if path, ok := sshProfilePathArg(arg, userProfile, ""); ok {
					*paths = append(*paths, path)
				}
			}
		}
	}
}

func sshIncludePaths(arg string, userProfile string, sshDir string) []string {
	pattern, ok := sshProfilePathArg(arg, userProfile, sshDir)
	if !ok {
		return nil
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return matches
}

func sshDirective(line string) (string, []string, bool) {
	words := sshWords(line)
	if len(words) == 0 {
		return "", nil, false
	}
	first := words[0]
	if key, value, ok := strings.Cut(first, "="); ok && key != "" {
		args := []string{}
		if value != "" {
			args = append(args, value)
		}
		args = append(args, words[1:]...)
		return key, args, true
	}
	key := words[0]
	args := append([]string(nil), words[1:]...)
	if len(args) > 0 {
		if value, ok := strings.CutPrefix(args[0], "="); ok {
			args[0] = value
		}
	}
	filtered := args[:0]
	for _, arg := range args {
		if arg != "" {
			filtered = append(filtered, arg)
		}
	}
	return key, filtered, true
}

func sshWords(line string) []string {
	var out []string
	var word strings.Builder
	var quote rune
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch {
		case ch == '#' && quote == 0:
			i = len(runes)
		case (ch == '\'' || ch == '"') && quote == ch:
			quote = 0
		case (ch == '\'' || ch == '"') && quote == 0:
			quote = ch
		case ch == '\\':
			if i+1 < len(runes) {
				next := runes[i+1]
				if next == '\'' || next == '"' || next == '\\' || (quote == 0 && next == ' ') {
					word.WriteRune(next)
					i++
				} else {
					word.WriteRune(ch)
				}
			} else {
				word.WriteRune(ch)
			}
		case ch == ' ' || ch == '\t' || ch == '\r':
			if quote == 0 {
				if word.Len() > 0 {
					out = append(out, word.String())
					word.Reset()
				}
			} else {
				word.WriteRune(ch)
			}
		default:
			word.WriteRune(ch)
		}
	}
	if word.Len() > 0 {
		out = append(out, word.String())
	}
	return out
}

func sshProfilePathArg(arg string, userProfile string, relativeBase string) (string, bool) {
	if strings.EqualFold(arg, "none") {
		return "", false
	}
	for _, exact := range []string{"~", "%d", "${HOME}"} {
		if arg == exact {
			return userProfile, true
		}
	}
	for _, prefix := range []string{"~/", `~\`, "%d/", `%d\`, "${HOME}/", `${HOME}\`} {
		if rest, ok := strings.CutPrefix(arg, prefix); ok {
			return filepath.Join(userProfile, rest), true
		}
	}
	if isWindowsSandboxAbs(arg) || filepath.IsAbs(arg) {
		return arg, true
	}
	if relativeBase != "" {
		return filepath.Join(relativeBase, arg), true
	}
	return "", false
}
