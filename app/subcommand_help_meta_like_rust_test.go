package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/cli"
	"codex_go/doctor"
)

// TestSubcommandHelpMetaLikeRust covers the help pages the generic clap router
// prints: the layout matches the captured page, stdout is the only stream
// touched, and the command exits 0.
func TestSubcommandHelpMetaLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "long page", args: []string{"mcp", "get", "--help"}, want: cli.SubcommandHelpText(&cli.SubcommandHelpRequest{Path: "mcp get"})},
		{name: "short page", args: []string{"mcp", "get", "-h"}, want: cli.SubcommandHelpText(&cli.SubcommandHelpRequest{Path: "mcp get", Short: true})},
		{name: "attached short page", args: []string{"mcp", "get", "-hx"}, want: cli.SubcommandHelpText(&cli.SubcommandHelpRequest{Path: "mcp get", Short: true})},
		{name: "flag before help", args: []string{"mcp", "list", "--json", "--help"}, want: cli.SubcommandHelpText(&cli.SubcommandHelpRequest{Path: "mcp list"})},
		{name: "help topic", args: []string{"help", "mcp", "get"}, want: cli.SubcommandHelpText(&cli.SubcommandHelpRequest{Path: "mcp get"})},
		{name: "root long before subcommand", args: []string{"--help", "exec"}, want: cli.RootHelpText(false)},
		{name: "root short before subcommand", args: []string{"-h", "exec"}, want: cli.RootHelpText(true)},
		{name: "root help topic", args: []string{"help", "exec"}, want: cli.SubcommandHelpText(&cli.SubcommandHelpRequest{Path: "exec"})},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := Run(context.Background(), testCase.args, strings.NewReader(""), &stdout, &stderr); err != nil {
				t.Fatalf("Run(%v) error = %v (stderr=%q)", testCase.args, err, stderr.String())
			}
			if stdout.String() != testCase.want {
				t.Fatalf("Run(%v) wrote %d bytes, want %d", testCase.args, stdout.Len(), len(testCase.want))
			}
			if stderr.Len() != 0 {
				t.Fatalf("Run(%v) wrote stderr %q", testCase.args, stderr.String())
			}
		})
	}
}

// TestRootVersionActionMetaLikeRust checks the root version action still fires
// when a subcommand follows the flag.
func TestRootVersionActionMetaLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	for _, args := range [][]string{{"-V", "exec"}, {"--version", "exec"}, {"-Vfoo"}} {
		var stdout, stderr bytes.Buffer
		if err := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr); err != nil {
			t.Fatalf("Run(%v) error = %v", args, err)
		}
		want := "codex-cli " + doctor.Version() + "\n"
		if stdout.String() != want {
			t.Fatalf("Run(%v) stdout = %q, want %q", args, stdout.String(), want)
		}
		if stderr.Len() != 0 {
			t.Fatalf("Run(%v) wrote stderr %q", args, stderr.String())
		}
	}
}

// TestSubcommandHelpDiagnosticsMetaLikeRust checks the clap blocks for a bad
// help value and an unknown help topic, including the exit status.
func TestSubcommandHelpDiagnosticsMetaLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	rootUsage := "Usage: codex [OPTIONS] [PROMPT]\n       codex [OPTIONS] <COMMAND> [ARGS]"
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "unexpected subcommand help value",
			args: []string{"mcp", "get", "--help=x"},
			want: "error: unexpected value 'x' for '--help' found; no more were expected\n\nUsage: codex mcp get --help <NAME>\n\nFor more information, try '--help'.\n",
		},
		{
			name: "unexpected root help value",
			args: []string{"--help=x"},
			want: "error: unexpected value 'x' for '--help' found; no more were expected\n\n" + rootUsage + "\n\nFor more information, try '--help'.\n",
		},
		{
			name: "unknown help topic",
			args: []string{"help", "bogus"},
			want: "error: unrecognized subcommand 'bogus'\n\n" + rootUsage + "\n\nFor more information, try '--help'.\n",
		},
		{
			name: "unknown nested help topic",
			args: []string{"help", "sandbox", "bogus"},
			want: "error: unrecognized subcommand 'bogus'\n\nUsage: codex sandbox [OPTIONS] [COMMAND]... [COMMAND]\n\nFor more information, try '--help'.\n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), testCase.args, strings.NewReader(""), &stdout, &stderr)
			if err == nil {
				t.Fatalf("Run(%v) returned nil error", testCase.args)
			}
			var exitErr *ExitError
			if !errors.As(err, &exitErr) || exitErr.exitCode() != 2 {
				t.Fatalf("Run(%v) error = %T %v, want exit status 2", testCase.args, err, err)
			}
			if stderr.String() != testCase.want {
				t.Fatalf("Run(%v) stderr = %q, want %q", testCase.args, stderr.String(), testCase.want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("Run(%v) wrote stdout %q", testCase.args, stdout.String())
			}
		})
	}
}
