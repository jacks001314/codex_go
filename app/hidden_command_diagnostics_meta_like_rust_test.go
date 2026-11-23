package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// TestHiddenCommandDiagnosticsMetaLikeRust checks the clap blocks for the
// execpolicy and tcp-tunnel argument surfaces, including the exit status Rust
// reports.
func TestHiddenCommandDiagnosticsMetaLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "execpolicy check missing both",
			args: []string{"execpolicy", "check"},
			want: "error: the following required arguments were not provided:\n  --rules <PATH>\n  <COMMAND>...\n\nUsage: codex execpolicy check --rules <PATH> <COMMAND>...\n\nFor more information, try '--help'.\n",
		},
		{
			name: "tcp-tunnel missing required",
			args: []string{"tcp-tunnel"},
			// Rust #61975 reduced the helper to one SSH route: either
			// --environment-id (optionally with --port) or a legacy --target
			// that requires --token-file.
			want: "error: the following required arguments were not provided:\n  --environment-id <ENVIRONMENT_ID>\n\nUsage: codex tcp-tunnel --environment-id <ENVIRONMENT_ID>\n\nFor more information, try '--help'.\n",
		},
		{
			name: "tcp-tunnel invalid proxy url",
			args: []string{"tcp-tunnel", "--proxy-url", "bad", "--environment-id", "env"},
			want: "error: invalid value 'bad' for '--proxy-url <PROXY_URL>': relative URL without a base\n\nFor more information, try '--help'.\n",
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
