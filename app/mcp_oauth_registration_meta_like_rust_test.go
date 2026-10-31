package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// TestMCPOAuthRegistrationMetaLikeRust checks the rendered clap blocks and exit
// statuses for `mcp add` / `mcp login` OAuth options.
func TestMCPOAuthRegistrationMetaLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	const hint = "\n\nFor more information, try '--help'.\n"
	cases := []struct {
		name   string
		args   []string
		stderr string
	}{
		{
			name:   "client id requires url",
			args:   []string{"mcp", "add", "fs", "--oauth-client-id", "c1", "--", "mcp-fs"},
			stderr: "error: the following required argument was not provided: url\n\nUsage: codex [OPTIONS] [PROMPT]\n       codex [OPTIONS] <COMMAND> [ARGS]" + hint,
		},
		{
			name:   "client secret requires url and client id",
			args:   []string{"mcp", "add", "fs", "--oauth-client-secret", "s", "--", "mcp-fs"},
			stderr: "error: the following required arguments were not provided:\n  --url <URL>\n  --oauth-client-id <CLIENT_ID>\n\nUsage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)" + hint,
		},
		{
			name:   "client secret requires client id",
			args:   []string{"mcp", "add", "fs", "--url", "https://x", "--oauth-client-secret", "s"},
			stderr: "error: the following required arguments were not provided:\n  --oauth-client-id <CLIENT_ID>\n\nUsage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)" + hint,
		},
		{
			name:   "registration invalid value",
			args:   []string{"mcp", "add", "fs", "--oauth-client-registration", "bogus", "--", "mcp-fs"},
			stderr: "error: invalid value 'bogus' for '--oauth-client-registration <AUTO|CIMD|DCR>'\n  [possible values: auto, cimd, dcr]" + hint,
		},
		{
			name:   "registration missing value",
			args:   []string{"mcp", "login", "fs", "--oauth-client-registration"},
			stderr: "error: a value is required for '--oauth-client-registration <AUTO|CIMD|DCR>' but none was supplied\n  [possible values: auto, cimd, dcr]" + hint,
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
			if stderr.String() != testCase.stderr {
				t.Fatalf("Run(%v) stderr = %q, want %q", testCase.args, stderr.String(), testCase.stderr)
			}
			if stdout.Len() != 0 {
				t.Fatalf("Run(%v) wrote stdout %q", testCase.args, stdout.String())
			}
		})
	}
}

// TestMCPOAuthRegistrationRunErrorsLikeRust keeps the run-time messages for the
// cases clap accepts but the command still rejects.
func TestMCPOAuthRegistrationRunErrorsLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "login registration unknown server",
			args: []string{"mcp", "login", "fs", "--oauth-client-registration", "auto"},
			want: "No MCP server named 'fs' found.",
		},
		{
			name: "empty client secret",
			args: []string{"mcp", "add", "fs", "--url", "https://x", "--oauth-client-id", "c1", "--oauth-client-secret", ""},
			want: "--oauth-client-secret must not be empty",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), testCase.args, strings.NewReader(""), &stdout, &stderr)
			if err == nil || err.Error() != testCase.want {
				t.Fatalf("Run(%v) error = %v, want %q", testCase.args, err, testCase.want)
			}
			if ExitCode(err) != 1 {
				t.Fatalf("Run(%v) exit code = %d, want 1", testCase.args, ExitCode(err))
			}
			if stdout.Len() != 0 {
				t.Fatalf("Run(%v) wrote stdout %q", testCase.args, stdout.String())
			}
		})
	}
}
