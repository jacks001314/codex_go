package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// TestMCPAddTransportGroupMetaLikeRust checks the rendered transport
// diagnostics and exit statuses for `mcp add`.
func TestMCPAddTransportGroupMetaLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	conflict := "error: the argument '--url <URL>' cannot be used with '[COMMAND]...'\n\nUsage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)\n\nFor more information, try '--help'.\n"
	cases := []struct {
		name   string
		args   []string
		stderr string
	}{
		{
			name:   "url conflicts with command",
			args:   []string{"mcp", "add", "fs", "--url", "https://x", "--", "mcp-fs"},
			stderr: conflict,
		},
		{
			name:   "url conflicts with command after env",
			args:   []string{"mcp", "add", "fs", "--url", "https://x", "--env", "K=V", "--", "echo", "hi"},
			stderr: conflict,
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

// TestMCPAddEnvWithoutCommandLikeRust keeps the run-time message clap leaves
// once --env selects the stdio transport.
func TestMCPAddEnvWithoutCommandLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"mcp", "add", "fs", "--url", "https://x", "--env", "K=V"},
		strings.NewReader(""), &stdout, &stderr)
	if err == nil || err.Error() != "command is required" {
		t.Fatalf("Run error = %v, want command is required", err)
	}
	if ExitCode(err) != 1 {
		t.Fatalf("Run exit code = %d, want 1", ExitCode(err))
	}
	if stdout.Len() != 0 {
		t.Fatalf("Run wrote stdout %q", stdout.String())
	}
}
