package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/cli"
)

// TestGroupHelpMetaLikeRust covers clap's group help action: `--help` / `help`
// print the long page and `-h` the short page on stdout with exit status 0,
// while a bare group command Rust declares with `arg_required_else_help` writes
// the short page to stderr and exits 2 without loading configuration.
func TestGroupHelpMetaLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "mcp long", args: []string{"mcp", "--help"}, want: cli.GroupHelpText(cli.GroupHelpMCP, false)},
		{name: "mcp short", args: []string{"mcp", "-h"}, want: cli.GroupHelpText(cli.GroupHelpMCP, true)},
		{name: "mcp help", args: []string{"mcp", "help"}, want: cli.GroupHelpText(cli.GroupHelpMCP, false)},
		{name: "features long", args: []string{"features", "--help"}, want: cli.GroupHelpText(cli.GroupHelpFeatures, false)},
		{name: "plugin long", args: []string{"plugin", "--help"}, want: cli.GroupHelpText(cli.GroupHelpPlugin, false)},
		{name: "plugin marketplace long", args: []string{"plugin", "marketplace", "--help"}, want: cli.GroupHelpText(cli.GroupHelpPluginMarketplace, false)},
		{name: "debug long", args: []string{"debug", "--help"}, want: cli.GroupHelpText(cli.GroupHelpDebug, false)},
		{name: "debug app-server short", args: []string{"debug", "app-server", "-h"}, want: cli.GroupHelpText(cli.GroupHelpDebugAppServer, true)},
		{name: "execpolicy long", args: []string{"execpolicy", "--help"}, want: cli.GroupHelpText(cli.GroupHelpExecpolicy, false)},
		{name: "app-server daemon long", args: []string{"app-server", "daemon", "--help"}, want: cli.GroupHelpText(cli.GroupHelpAppServerDaemon, false)},
		{name: "help action ignores trailing tokens", args: []string{"plugin", "--help", "--bogus"}, want: cli.GroupHelpText(cli.GroupHelpPlugin, false)},
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

// TestBareGroupWritesShortHelpToStderrLikeRust pins clap's
// arg_required_else_help behaviour: the short page goes to stderr and the exit
// status is 2, with nothing on stdout.
func TestBareGroupWritesShortHelpToStderrLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	tests := []struct {
		name string
		args []string
		kind cli.GroupHelpKind
	}{
		{name: "mcp", args: []string{"mcp"}, kind: cli.GroupHelpMCP},
		{name: "features", args: []string{"features"}, kind: cli.GroupHelpFeatures},
		{name: "plugin", args: []string{"plugin"}, kind: cli.GroupHelpPlugin},
		{name: "plugin marketplace", args: []string{"plugin", "marketplace"}, kind: cli.GroupHelpPluginMarketplace},
		{name: "debug", args: []string{"debug"}, kind: cli.GroupHelpDebug},
		{name: "debug app-server", args: []string{"debug", "app-server"}, kind: cli.GroupHelpDebugAppServer},
		{name: "execpolicy", args: []string{"execpolicy"}, kind: cli.GroupHelpExecpolicy},
		{name: "app-server daemon", args: []string{"app-server", "daemon"}, kind: cli.GroupHelpAppServerDaemon},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), testCase.args, strings.NewReader(""), &stdout, &stderr)
			var exitErr *ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("Run(%v) error = %v, want an ExitError", testCase.args, err)
			}
			if exitErr.Code != 2 || !exitErr.Silent {
				t.Fatalf("Run(%v) exit error = %#v, want silent 2", testCase.args, exitErr)
			}
			if stdout.Len() != 0 {
				t.Fatalf("Run(%v) wrote stdout %q", testCase.args, stdout.String())
			}
			if want := cli.GroupHelpText(testCase.kind, true); stderr.String() != want {
				t.Fatalf("Run(%v) stderr = %d bytes, want %d", testCase.args, stderr.Len(), len(want))
			}
		})
	}
}
