package cli

import (
	"errors"
	"strings"
	"testing"
)

// TestSubcommandHelpRequestsLikeRust pins clap's help action for every page the
// CLI captures: `-h` prints the short layout, `--help` the long one, and
// `help <topic>` the long page of a nested command.
func TestSubcommandHelpRequestsLikeRust(t *testing.T) {
	pages := 0
	for path := range subcommandHelpPages {
		if path == "" {
			continue
		}
		pages++
		words := strings.Fields(path)
		command := words[0]
		args := append([]string(nil), words[1:]...)
		t.Run(strings.ReplaceAll(path, " ", "_"), func(t *testing.T) {
			long := SubcommandHelpRequestFor(command, append(append([]string(nil), args...), "--help"))
			if long == nil || long.Path != path || long.Short || long.UnexpectedValue != nil {
				t.Fatalf("%s --help = %#v, want long %q", path, long, path)
			}
			short := SubcommandHelpRequestFor(command, append(append([]string(nil), args...), "-h"))
			if short == nil || short.Path != path || !short.Short {
				t.Fatalf("%s -h = %#v, want short %q", path, short, path)
			}
			if len(words) == 1 {
				return
			}
			topicArgs := append(append([]string(nil), args[:len(args)-1]...), "help", words[len(words)-1])
			topic := SubcommandHelpRequestFor(command, topicArgs)
			if topic == nil || topic.Path != path || topic.Short {
				t.Fatalf("%s help topic = %#v, want long %q", path, topic, path)
			}
		})
	}
	if pages != 79 {
		t.Fatalf("captured %d subcommand pages, want 79 (plus the root page)", pages)
	}
}

// TestSubcommandHelpPagesArePopulatedLikeRust checks every rendered page is a
// newline-terminated block and that the usage tables cover the same paths.
func TestSubcommandHelpPagesArePopulatedLikeRust(t *testing.T) {
	if len(subcommandHelpPages) != 80 || len(subcommandShortHelpPages) != 80 {
		t.Fatalf("captured %d/%d pages, want 80/80", len(subcommandHelpPages), len(subcommandShortHelpPages))
	}
	for path, lines := range subcommandHelpPages {
		if len(lines) == 0 {
			t.Fatalf("page %q is empty", path)
		}
		page := SubcommandHelpText(&SubcommandHelpRequest{Path: path})
		if !strings.HasSuffix(page, "\n") || strings.HasSuffix(page, "\n\n") {
			t.Fatalf("page %q does not end with exactly one newline", path)
		}
		if short := SubcommandHelpText(&SubcommandHelpRequest{Path: path, Short: true}); short == "" {
			t.Fatalf("short page %q is empty", path)
		}
		if usage := subcommandHelpUsages[path]; !strings.HasPrefix(usage, "Usage: ") {
			t.Fatalf("usage for %q = %q", path, usage)
		}
		if usage := subcommandHelpValueUsages[path]; !strings.HasPrefix(usage, "Usage: ") {
			t.Fatalf("help-value usage for %q = %q", path, usage)
		}
	}
	if SubcommandHelpText(nil) != "" {
		t.Fatal("SubcommandHelpText(nil) is not empty")
	}
}

// TestSubcommandHelpIgnoresNonHelpArgumentsLikeRust checks the tokens clap
// binds as values instead of treating them as the help action.
func TestSubcommandHelpIgnoresNonHelpArgumentsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    []string
	}{
		{name: "unknown flag before help", command: "mcp", args: []string{"add", "--bogus", "--help"}},
		{name: "separator before help", command: "sandbox", args: []string{"--", "--help"}},
		{name: "value flag consumes help", command: "queue", args: []string{"--thread", "--help"}},
		{name: "short value flag consumes help", command: "mcp", args: []string{"get", "-c", "--help"}},
		{name: "inline value consumes help", command: "mcp", args: []string{"get", "--config=--help"}},
		{name: "unknown help topic", command: "mcp", args: []string{"help", "bogus"}},
		{name: "hyphen value positional", command: "execpolicy", args: []string{"check", "-hx"}},
		{name: "no arguments", command: "mcp", args: nil},
		{name: "plain positional", command: "apply", args: []string{"foo"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if request := SubcommandHelpRequestFor(testCase.command, testCase.args); request != nil {
				t.Fatalf("%s %v = %#v, want no help action", testCase.command, testCase.args, request)
			}
		})
	}

	positive := []struct {
		name    string
		command string
		args    []string
		path    string
	}{
		{name: "flag before help", command: "mcp", args: []string{"list", "--json", "--help"}, path: "mcp list"},
		{name: "inline flag value", command: "mcp", args: []string{"list", "--json=1", "-h"}, path: "mcp list"},
		{name: "short option value", command: "mcp", args: []string{"get", "-c", "foo", "--help"}, path: "mcp get"},
		{name: "attached short value", command: "mcp", args: []string{"get", "-cfoo", "--help"}, path: "mcp get"},
	}
	for _, testCase := range positive {
		t.Run(testCase.name, func(t *testing.T) {
			request := SubcommandHelpRequestFor(testCase.command, testCase.args)
			if request == nil || request.Path != testCase.path {
				t.Fatalf("%s %v = %#v, want %q", testCase.command, testCase.args, request, testCase.path)
			}
		})
	}
}

// TestSubcommandHelpValueDiagnosticsLikeRust covers `--help=<value>`, which
// clap rejects instead of printing the page.
func TestSubcommandHelpValueDiagnosticsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		args []string
		want string
	}{
		{args: []string{"get", "--help=x"}, want: "x"},
		{args: []string{"get", "--help="}, want: ""},
	} {
		request := SubcommandHelpRequestFor("mcp", testCase.args)
		if request == nil || request.UnexpectedValue == nil || *request.UnexpectedValue != testCase.want {
			t.Fatalf("mcp %v = %#v, want unexpected value %q", testCase.args, request, testCase.want)
		}
	}

	parsed, err := Parse([]string{"mcp", "get", "--help=x"})
	if parsed != nil {
		t.Fatalf("Parse returned %#v with the diagnostic", parsed)
	}
	var usage *UsageError
	if !errors.As(err, &usage) {
		t.Fatalf("Parse error = %T %v, want UsageError", err, err)
	}
	if want := "unexpected value 'x' for '--help' found; no more were expected"; usage.Message != want {
		t.Fatalf("message = %q, want %q", usage.Message, want)
	}
	if want := "Usage: codex mcp get --help <NAME>"; usage.Usage != want {
		t.Fatalf("usage = %q, want %q", usage.Usage, want)
	}
}

// TestExecHelpPagesMatchSubcommandPagesLikeRust keeps `codex help exec` and
// `codex exec --help` on the same bytes.
func TestExecHelpPagesMatchSubcommandPagesLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		kind ExecHelpKind
		path string
	}{
		{kind: ExecHelpExec, path: "exec"},
		{kind: ExecHelpResume, path: "exec resume"},
		{kind: ExecHelpFork, path: "exec fork"},
		{kind: ExecHelpReview, path: "exec review"},
	} {
		for _, short := range []bool{false, true} {
			want := SubcommandHelpText(&SubcommandHelpRequest{Path: testCase.path, Short: short})
			if got := ExecHelpText(testCase.kind, short); got != want {
				t.Fatalf("ExecHelpText(%q, %v) differs from the captured page", testCase.kind, short)
			}
		}
	}
}

// TestParseSubcommandHelpPrecedenceLikeRust records which router owns a command:
// groups render their own pages, and the exec family keeps exec_help.go.
func TestParseSubcommandHelpPrecedenceLikeRust(t *testing.T) {
	parsed, err := Parse([]string{"mcp", "list", "--help"})
	if err != nil {
		t.Fatalf("Parse(mcp list --help) error = %v", err)
	}
	if parsed.SubcommandHelp == nil || parsed.SubcommandHelp.Path != "mcp list" {
		t.Fatalf("mcp list --help = %#v", parsed.SubcommandHelp)
	}

	parsed, err = Parse([]string{"sandbox", "uninstall", "-h"})
	if err != nil {
		t.Fatalf("Parse(sandbox uninstall -h) error = %v", err)
	}
	if parsed.SubcommandHelp == nil || !parsed.SubcommandHelp.Short || parsed.SubcommandHelp.Path != "sandbox uninstall" {
		t.Fatalf("sandbox uninstall -h = %#v", parsed.SubcommandHelp)
	}

	parsed, err = Parse([]string{"mcp", "--help"})
	if err != nil {
		t.Fatalf("Parse(mcp --help) error = %v", err)
	}
	if parsed.GroupHelp == nil || parsed.SubcommandHelp != nil {
		t.Fatalf("mcp --help = group %#v sub %#v, want the group page", parsed.GroupHelp, parsed.SubcommandHelp)
	}

	parsed, err = Parse([]string{"exec", "--help"})
	if err != nil {
		t.Fatalf("Parse(exec --help) error = %v", err)
	}
	if parsed.SubcommandHelp != nil {
		t.Fatalf("exec --help = %#v, want the exec family page", parsed.SubcommandHelp)
	}
	if parsed.Exec.Help == nil || parsed.Exec.Help.Kind != ExecHelpExec {
		t.Fatalf("exec --help = %#v, want the exec page", parsed.Exec.Help)
	}
}
