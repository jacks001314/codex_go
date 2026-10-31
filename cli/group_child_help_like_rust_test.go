package cli

import (
	"strings"
	"testing"
)

// TestHiddenDebugHelpPagesLikeRust pins the clap help pages of the two hidden
// debug subcommands, which the group page does not list.
func TestHiddenDebugHelpPagesLikeRust(t *testing.T) {
	cases := []struct{ path, want string }{
		{"debug clear-memories", "Internal: reset local memory state for a fresh start"},
		{"debug trace-reduce", "Replay a rollout trace bundle and write reduced state JSON"},
	}
	for _, testCase := range cases {
		long := SubcommandHelpText(&SubcommandHelpRequest{Path: testCase.path})
		if !strings.HasPrefix(long, testCase.want+"\n") {
			t.Fatalf("%s long help = %q", testCase.path, long)
		}
		short := SubcommandHelpText(&SubcommandHelpRequest{Path: testCase.path, Short: true})
		if !strings.HasPrefix(short, testCase.want+"\n") {
			t.Fatalf("%s short help = %q", testCase.path, short)
		}
	}
}

// TestGroupUnknownChildWithHelpErrorsLikeRust pins clap's order of operations:
// a group resolves its child before the help action runs, so an unknown bare
// word followed by `--help` is an unrecognized-subcommand diagnostic.
func TestGroupUnknownChildWithHelpErrorsLikeRust(t *testing.T) {
	cases := [][]string{
		{"debug", "config", "--help"},
		{"debug", "nosuch", "--help"},
		{"mcp", "nosuch", "--help"},
		{"plugin", "nosuch", "--help"},
		{"features", "nosuch", "--help"},
		{"app-server", "nosuch", "--help"},
	}
	for _, args := range cases {
		_, err := Parse(args)
		if err == nil {
			t.Fatalf("Parse(%v) error = nil", args)
		}
		want := "unrecognized subcommand '" + args[1] + "'"
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Parse(%v) error = %v, want %q", args, err, want)
		}
	}
}
