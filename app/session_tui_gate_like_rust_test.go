package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The cases below were captured from the 0.166 binary with an empty CODEX_HOME
// and no terminal on stdin (out/round43-gate-cases.json): Rust routes `resume`
// and `fork` through run_interactive_tui, so the --no-daemon/--remote
// combination and the TERM gate answer before the session store is touched, and
// tui::init then refuses a non-terminal stdin.
func TestSessionTUIStartGateLikeRust(t *testing.T) {
	cases := []struct {
		args       []string
		term       string
		wantExit   int
		wantStderr string
		wantErr    string
	}{
		{args: []string{"resume"}, term: "dumb", wantExit: 1, wantStderr: "ERROR: TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.\n", wantErr: ""},
		{args: []string{"resume"}, term: "", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"resume"}, term: "xterm-256color", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"resume", "abc"}, term: "dumb", wantExit: 1, wantStderr: "ERROR: TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.\n", wantErr: ""},
		{args: []string{"resume", "abc"}, term: "", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"resume", "abc"}, term: "xterm-256color", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"resume", "--last"}, term: "dumb", wantExit: 1, wantStderr: "ERROR: TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.\n", wantErr: ""},
		{args: []string{"resume", "--last"}, term: "", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"resume", "--last"}, term: "xterm-256color", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"resume", "--all"}, term: "dumb", wantExit: 1, wantStderr: "ERROR: TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.\n", wantErr: ""},
		{args: []string{"resume", "--all"}, term: "", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"resume", "--all"}, term: "xterm-256color", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"resume", "--include-non-interactive"}, term: "dumb", wantExit: 1, wantStderr: "ERROR: TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.\n", wantErr: ""},
		{args: []string{"resume", "--include-non-interactive"}, term: "", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"resume", "--include-non-interactive"}, term: "xterm-256color", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"resume", "abc", "continue here"}, term: "dumb", wantExit: 1, wantStderr: "ERROR: TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.\n", wantErr: ""},
		{args: []string{"resume", "abc", "continue here"}, term: "", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"resume", "abc", "continue here"}, term: "xterm-256color", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"fork"}, term: "dumb", wantExit: 1, wantStderr: "ERROR: TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.\n", wantErr: ""},
		{args: []string{"fork"}, term: "", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"fork"}, term: "xterm-256color", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"fork", "abc"}, term: "dumb", wantExit: 1, wantStderr: "ERROR: TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.\n", wantErr: ""},
		{args: []string{"fork", "abc"}, term: "", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"fork", "abc"}, term: "xterm-256color", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"fork", "--last"}, term: "dumb", wantExit: 1, wantStderr: "ERROR: TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.\n", wantErr: ""},
		{args: []string{"fork", "--last"}, term: "", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"fork", "--last"}, term: "xterm-256color", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"fork", "--all"}, term: "dumb", wantExit: 1, wantStderr: "ERROR: TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.\n", wantErr: ""},
		{args: []string{"fork", "--all"}, term: "", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"fork", "--all"}, term: "xterm-256color", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"resume", "--no-daemon"}, term: "dumb", wantExit: 1, wantStderr: "ERROR: TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.\n", wantErr: ""},
		{args: []string{"fork", "--no-daemon"}, term: "", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
		{args: []string{"--no-daemon", "resume", "--remote", "ws://x"}, term: "dumb", wantExit: 1, wantStderr: "ERROR: --no-daemon cannot be used with --remote.\n", wantErr: ""},
		{args: []string{"--no-daemon", "fork", "--remote", "ws://127.0.0.1:1234"}, term: "", wantExit: 1, wantStderr: "ERROR: --no-daemon cannot be used with --remote.\n", wantErr: ""},
		{args: []string{"resume", "--remote-auth-token-env", "X"}, term: "dumb", wantExit: 1, wantStderr: "ERROR: TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.\n", wantErr: ""},
		{args: []string{"resume", "--remote-auth-token-env", "X"}, term: "", wantExit: 1, wantStderr: "ERROR: `--remote-auth-token-env` requires `--remote`.\n", wantErr: ""},
		{args: []string{"fork", "--remote-auth-token-env", "X"}, term: "", wantExit: 1, wantStderr: "ERROR: `--remote-auth-token-env` requires `--remote`.\n", wantErr: ""},
		{args: []string{"--no-daemon", "resume", "--remote-auth-token-env", "X"}, term: "", wantExit: 1, wantStderr: "ERROR: `--remote-auth-token-env` requires `--remote`.\n", wantErr: ""},
		{args: []string{"resume", "--config="}, term: "dumb", wantExit: 1, wantStderr: "ERROR: TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.\n", wantErr: ""},
		{args: []string{"resume", "--config="}, term: "", wantExit: 1, wantStderr: "", wantErr: "Error parsing -c overrides: Invalid override (missing '='): "},
		{args: []string{"fork", "--config="}, term: "", wantExit: 1, wantStderr: "", wantErr: "Error parsing -c overrides: Invalid override (missing '='): "},
		{args: []string{"archive", "--config=", "x"}, term: "", wantExit: 1, wantStderr: "", wantErr: "failed to parse -c overrides: Invalid override (missing '='): "},
		{args: []string{"unarchive", "--config=", "x"}, term: "", wantExit: 1, wantStderr: "", wantErr: "failed to parse -c overrides: Invalid override (missing '='): "},
		{args: []string{"delete", "x", "--force", "--config="}, term: "", wantExit: 1, wantStderr: "", wantErr: "--force requires a session UUID; names must be confirmed interactively"},
		{args: []string{"queue", "--thread", "t", "--message", "m", "--config="}, term: "", wantExit: 1, wantStderr: "", wantErr: "failed to parse -c overrides: Invalid override (missing '='): "},
		{args: []string{"resume", "--remote", "ws://bad-host:1", "--last"}, term: "", wantExit: 1, wantStderr: "", wantErr: "stdin is not a terminal"},
	}
	for _, tc := range cases {
		label := strings.Join(tc.args, " ") + " term=" + tc.term
		t.Setenv("TERM", tc.term)
		t.Setenv("CODEX_HOME", t.TempDir())
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), tc.args, strings.NewReader(""), &stdout, &stderr)
		if got := ExitCode(err); got != tc.wantExit {
			t.Errorf("%s: exit = %d, want %d (err=%v, stderr=%q)", label, got, tc.wantExit, err, stderr.String())
		}
		if got := stderr.String(); got != tc.wantStderr {
			t.Errorf("%s: stderr = %q, want %q", label, got, tc.wantStderr)
		}
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("%s: err = %v, want %q", label, err, tc.wantErr)
			}
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: stdout = %q, want empty", label, stdout.String())
		}
	}
}
