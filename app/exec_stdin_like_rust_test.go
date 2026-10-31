package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/prompt"
)

// TestExecStdinPromptNoticesLikeRust pins the Rust exec stdin contract: the
// notices go to stderr before the prompt is resolved, the prompt is resolved
// before the output schema is loaded, and the direct `eprintln!; exit(1)`
// messages stay unprefixed.
func TestExecStdinPromptNoticesLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", t.TempDir())
	dir := t.TempDir()
	base := []string{"exec", "--skip-git-repo-check", "-C", dir}
	tests := []struct {
		name        string
		args        []string
		stdin       string
		wantStderr  string
		wantErr     string
		errContains bool
	}{
		{
			name:       "empty pipe",
			args:       base,
			wantStderr: "Reading prompt from stdin...\n",
			wantErr:    "No prompt provided via stdin.",
		},
		{
			name:       "blank pipe",
			args:       base,
			stdin:      "   \n",
			wantStderr: "Reading prompt from stdin...\n",
			wantErr:    "No prompt provided via stdin.",
		},
		{
			name:       "forced stdin sentinel",
			args:       append(append([]string(nil), base...), "-"),
			wantErr:    "No prompt provided via stdin.",
			wantStderr: "",
		},
		{
			name:        "schema error follows the append notice",
			args:        append(append([]string(nil), base...), "--output-schema", "missing.json", "hi"),
			wantStderr:  "Reading additional input from stdin...\n",
			wantErr:     "Failed to read output schema file missing.json: ",
			errContains: true,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			err := Run(context.Background(), testCase.args, strings.NewReader(testCase.stdin), &stdout, &stderr)
			if err == nil {
				t.Fatalf("Run(%v) returned nil error", testCase.args)
			}
			if stderr.String() != testCase.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), testCase.wantStderr)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if testCase.errContains {
				if !strings.HasPrefix(err.Error(), testCase.wantErr) {
					t.Fatalf("error = %q, want prefix %q", err.Error(), testCase.wantErr)
				}
			} else if err.Error() != testCase.wantErr {
				t.Fatalf("error = %q, want %q", err.Error(), testCase.wantErr)
			}
			// Rust prints these with eprintln! before exiting, never through
			// anyhow, so the entry point must not add the "Error: " prefix.
			if _, ok := DirectExitMessage(err); !ok && !errors.As(err, new(*prompt.DirectExitError)) {
				t.Fatalf("error %T does not carry a direct exit message", err)
			}
		})
	}
}
