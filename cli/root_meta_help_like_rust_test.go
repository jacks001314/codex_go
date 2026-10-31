package cli

import (
	"errors"
	"testing"
)

// TestRootHelpActionsLikeRust pins clap's root-level help action: the flag fires
// while the root command is parsed, so a trailing subcommand never runs, and
// `help <topic>` prints the topic's long page.
func TestRootHelpActionsLikeRust(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		short bool
		topic string
	}{
		{name: "short flag", args: []string{"-h"}, short: true},
		{name: "short before subcommand", args: []string{"-h", "exec"}, short: true},
		{name: "short cluster", args: []string{"-hx", "exec"}, short: true},
		{name: "long short spelling", args: []string{"-help"}, short: true},
		{name: "long flag", args: []string{"--help", "exec"}},
		{name: "long after options", args: []string{"-c", "foo", "--help"}},
		{name: "long before options", args: []string{"--help", "-c", "foo"}},
		{name: "bare help", args: []string{"help"}},
		{name: "help topic", args: []string{"help", "exec"}, topic: "exec"},
		{name: "help nested topic", args: []string{"help", "mcp", "get"}, topic: "mcp get"},
		{name: "help after options", args: []string{"-c", "foo", "help", "mcp"}, topic: "mcp"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := Parse(testCase.args)
			if err != nil {
				t.Fatalf("Parse(%v) error = %v", testCase.args, err)
			}
			request := parsed.SubcommandHelp
			if request == nil {
				t.Fatalf("Parse(%v) SubcommandHelp = nil", testCase.args)
			}
			if request.Path != testCase.topic || request.Short != testCase.short {
				t.Fatalf("Parse(%v) = %#v, want path %q short %v", testCase.args, request, testCase.topic, testCase.short)
			}
			if parsed.RootVersion {
				t.Fatalf("Parse(%v) also asked for the version", testCase.args)
			}
		})
	}
}

// TestRootVersionActionsLikeRust covers clap's version action, which likewise
// fires before any subcommand runs.
func TestRootVersionActionsLikeRust(t *testing.T) {
	for _, args := range [][]string{{"-V"}, {"-V", "exec"}, {"-Vfoo"}, {"--version", "exec"}} {
		parsed, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v) error = %v", args, err)
		}
		if !parsed.RootVersion {
			t.Fatalf("Parse(%v) RootVersion = false", args)
		}
		if parsed.SubcommandHelp != nil {
			t.Fatalf("Parse(%v) SubcommandHelp = %#v", args, parsed.SubcommandHelp)
		}
	}
}

// TestRootHelpDiagnosticsLikeRust covers the diagnostics clap prints for a bad
// help value and for an unknown `help <topic>` segment.
func TestRootHelpDiagnosticsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "unexpected help value",
			args:    []string{"--help=x"},
			message: "unexpected value 'x' for '--help' found; no more were expected",
			usage:   rootUsageText,
		},
		{
			name:    "empty help value",
			args:    []string{"--help="},
			message: "unexpected value '' for '--help' found; no more were expected",
			usage:   rootUsageText,
		},
		{
			name:    "unexpected version value",
			args:    []string{"--version=1"},
			message: "unexpected value '1' for '--version' found; no more were expected",
			usage:   rootUsageText,
		},
		{
			name:    "unknown help topic",
			args:    []string{"help", "bogus"},
			message: "unrecognized subcommand 'bogus'",
			usage:   rootUsageText,
		},
		{
			name:    "flag as help topic",
			args:    []string{"help", "--help"},
			message: "unrecognized subcommand '--help'",
			usage:   rootUsageText,
		},
		{
			name:    "unknown nested help topic",
			args:    []string{"help", "sandbox", "bogus"},
			message: "unrecognized subcommand 'bogus'",
			usage:   subcommandHelpUsages["sandbox"],
		},
		{
			name:    "unknown topic below leaf",
			args:    []string{"help", "mcp", "get", "extra"},
			message: "unrecognized subcommand 'extra'",
			usage:   subcommandHelpUsages["mcp get"],
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := Parse(testCase.args)
			if parsed != nil {
				t.Fatalf("Parse(%v) returned %#v with the diagnostic", testCase.args, parsed)
			}
			var usage *UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("Parse(%v) error = %T %v, want UsageError", testCase.args, err, err)
			}
			if usage.Message != testCase.message {
				t.Fatalf("Parse(%v) message = %q, want %q", testCase.args, usage.Message, testCase.message)
			}
			if usage.Usage != testCase.usage {
				t.Fatalf("Parse(%v) usage = %q, want %q", testCase.args, usage.Usage, testCase.usage)
			}
		})
	}
}
