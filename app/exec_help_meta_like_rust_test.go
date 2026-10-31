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

// TestExecHelpFlagsExitBeforeStartupLikeRust covers clap's help action for the
// exec family: Rust prints the page and exits 0 without loading configuration
// or authenticating, so no startup diagnostics may leak into stderr.
func TestExecHelpFlagsExitBeforeStartupLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "exec long", args: []string{"exec", "--help"}, want: cli.ExecHelpText(cli.ExecHelpExec, false)},
		{name: "exec short", args: []string{"exec", "-h"}, want: cli.ExecHelpText(cli.ExecHelpExec, true)},
		{name: "exec help", args: []string{"exec", "help"}, want: cli.ExecHelpText(cli.ExecHelpExec, false)},
		{name: "resume long", args: []string{"exec", "resume", "--help"}, want: cli.ExecHelpText(cli.ExecHelpResume, false)},
		{name: "resume short", args: []string{"exec", "resume", "-h"}, want: cli.ExecHelpText(cli.ExecHelpResume, true)},
		{name: "fork long", args: []string{"exec", "fork", "--help"}, want: cli.ExecHelpText(cli.ExecHelpFork, false)},
		{name: "fork short", args: []string{"exec", "fork", "-h"}, want: cli.ExecHelpText(cli.ExecHelpFork, true)},
		{name: "review long", args: []string{"exec", "review", "--help"}, want: cli.ExecHelpText(cli.ExecHelpReview, false)},
		{name: "review short", args: []string{"exec", "review", "-h"}, want: cli.ExecHelpText(cli.ExecHelpReview, true)},
		{name: "help topic", args: []string{"exec", "help", "fork"}, want: cli.ExecHelpText(cli.ExecHelpFork, false)},
	}
	for _, testCase := range tests {
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

func TestExecVersionFlagLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	for _, args := range [][]string{{"exec", "-V"}, {"exec", "--version"}, {"exec", "resume", "-V"}} {
		var stdout, stderr bytes.Buffer
		if err := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr); err != nil {
			t.Fatalf("Run(%v) error = %v", args, err)
		}
		want := "codex-cli-exec " + doctor.Version() + "\n"
		if stdout.String() != want {
			t.Fatalf("Run(%v) stdout = %q, want %q", args, stdout.String(), want)
		}
	}
}

// TestExecClapDiagnosticsExitTwoLikeRust checks the rendered clap block and the
// exit status Rust reports for exec argument errors.
func TestExecClapDiagnosticsExitTwoLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "unexpected argument",
			args: []string{"exec", "--bogus"},
			want: "error: unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'\n\nUsage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]\n\nFor more information, try '--help'.\n",
		},
		{
			name: "unrecognized help topic",
			args: []string{"exec", "help", "bogus"},
			want: "error: unrecognized subcommand 'bogus'\n\nUsage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]\n\nFor more information, try '--help'.\n",
		},
		{
			name: "approve-for-me with sandbox",
			args: []string{"exec", "--approve-for-me", "-s", "read-only"},
			want: "error: the argument '--approve-for-me' cannot be used with '--sandbox <SANDBOX_MODE>'\n\nUsage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]\n\nFor more information, try '--help'.\n",
		},
		{
			name: "invalid sandbox value",
			args: []string{"exec", "--sandbox", "bogus"},
			want: "error: invalid value 'bogus' for '--sandbox <SANDBOX_MODE>'\n  [possible values: read-only, workspace-write, danger-full-access]\n\nFor more information, try '--help'.\n",
		},
	}
	for _, testCase := range tests {
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
