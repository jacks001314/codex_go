package parity

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	codexcli "codex_go/internal/cli"
)

func TestRustCliSubcommandSurfaceAgainstGoParser(t *testing.T) {
	root := rustSnapshotRoot(t)
	cmds, aliases := rustCliSubcommandSurface(t, filepath.Join(root, "cli", "src", "main.rs"))

	for _, tc := range append(cmdSurfaceCases(cmds), aliasSurfaceCases(aliases)...) {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := codexcli.Parse(tc.args); err != nil {
				t.Fatalf("Go parser rejected Rust CLI surface %q with %v: %v", tc.name, tc.args, err)
			}
		})
	}
}

type cliSurfaceCase struct {
	name string
	args []string
}

func cmdSurfaceCases(cmds []string) []cliSurfaceCase {
	cases := make([]cliSurfaceCase, 0, len(cmds))
	for _, cmd := range cmds {
		switch cmd {
		case "exec":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "hello"}})
		case "review":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "--uncommitted"}})
		case "login":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "status"}})
		case "logout":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd}})
		case "mcp":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "list"}})
		case "plugin":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "list"}})
		case "mcp-server":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd}})
		case "app-server":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "--listen", "off"}})
		case "remote-control":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "pair"}})
		case "app":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "."}})
		case "completion":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "bash"}})
		case "update":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd}})
		case "doctor":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "--summary"}})
		case "sandbox":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "--", "echo"}})
		case "debug":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "models"}})
		case "execpolicy":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "check", "--rules", "policy.rules", "echo"}})
		case "apply":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "*** Begin Patch\n*** End Patch"}})
		case "resume":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "--last"}})
		case "archive":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "thread-1"}})
		case "delete":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "thread-1"}})
		case "unarchive":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "thread-1"}})
		case "fork":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "--last"}})
		case "cloud":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "status", "task-1"}})
		case "responses-api-proxy":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "--port", "3456"}})
		case "stdio-to-uds":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, `\\.\pipe\codex-test`}})
		case "exec-server":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "--listen", "stdio"}})
		case "features":
			cases = append(cases, cliSurfaceCase{name: cmd, args: []string{cmd, "list"}})
		}
	}
	return cases
}

func aliasSurfaceCases(aliases []string) []cliSurfaceCase {
	cases := make([]cliSurfaceCase, 0, len(aliases))
	for _, alias := range aliases {
		switch alias {
		case "e":
			cases = append(cases, cliSurfaceCase{name: alias, args: []string{alias, "hello"}})
		case "a":
			cases = append(cases, cliSurfaceCase{name: alias, args: []string{alias, "*** Begin Patch\n*** End Patch"}})
		case "cloud-tasks":
			cases = append(cases, cliSurfaceCase{name: alias, args: []string{alias, "status", "task-1"}})
		}
	}
	return cases
}

func rustCliSubcommandSurface(t *testing.T, path string) ([]string, []string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	source := string(data)

	enumBlock := regexp.MustCompile(`(?s)enum Subcommand \{(.*?)\n\}`)
	block := enumBlock.FindStringSubmatch(source)
	if len(block) != 2 {
		t.Fatalf("could not locate Rust Subcommand enum in %s", path)
	}

	visibleAlias := regexp.MustCompile(`visible_alias\s*=\s*"([^"]+)"`)
	clapName := regexp.MustCompile(`name\s*=\s*"([^"]+)"`)
	var commands []string
	for _, line := range strings.Split(block[1], "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#[") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(trimmed, "App(") {
			commands = append(commands, "app")
			continue
		}
		if match := regexp.MustCompile(`^([A-Z][A-Za-z0-9_]*)\(`).FindStringSubmatch(trimmed); len(match) == 2 {
			commands = append(commands, rustKebabCase(match[1]))
		}
	}

	var aliases []string
	for _, match := range visibleAlias.FindAllStringSubmatch(source, -1) {
		aliases = append(aliases, match[1])
	}
	for _, match := range clapName.FindAllStringSubmatch(source, -1) {
		if match[1] == "cloud" || match[1] == "stdio-to-uds" {
			aliases = append(aliases, match[1])
		}
	}

	sort.Strings(commands)
	sort.Strings(aliases)
	return dedupeStrings(commands), dedupeStrings(aliases)
}

func rustKebabCase(name string) string {
	var out []rune
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			out = append(out, '-')
		}
		if r >= 'A' && r <= 'Z' {
			out = append(out, r+'a'-'A')
			continue
		}
		out = append(out, r)
	}
	return strings.ToLower(string(out))
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for i, value := range values {
		if i == 0 || value != values[i-1] {
			out = append(out, value)
		}
	}
	return out
}
