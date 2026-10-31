package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// TestHiddenCommandDiagnosticsMetaLikeRust checks the clap blocks for the
// execpolicy, stdio-to-uds and tcp-tunnel argument surfaces, including the exit
// status Rust reports.
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
			name: "stdio-to-uds unknown flag",
			args: []string{"stdio-to-uds", "--bogus"},
			want: "error: unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'\n\nUsage: codex stdio-to-uds [OPTIONS] <SOCKET_PATH>\n\nFor more information, try '--help'.\n",
		},
		{
			name: "tcp-tunnel missing required",
			args: []string{"tcp-tunnel"},
			want: "error: the following required arguments were not provided:\n  --proxy-url <PROXY_URL>\n  --proxy-origins-file <PROXY_ORIGINS_FILE>\n  --target <TARGET>\n\nUsage: codex tcp-tunnel --proxy-url <PROXY_URL> --proxy-origins-file <PROXY_ORIGINS_FILE> --target <TARGET>\n\nFor more information, try '--help'.\n",
		},
		{
			name: "tcp-tunnel invalid proxy url",
			args: []string{"tcp-tunnel", "--proxy-url", "bad", "--proxy-origins-file", "y", "--target", "z"},
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
