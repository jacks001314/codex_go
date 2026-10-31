package cli

import (
	"errors"
	"testing"
)

func parseUsageError(t *testing.T, args []string) *UsageError {
	t.Helper()
	_, err := Parse(args)
	var usageErr *UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("Parse(%v) error = %v, want a UsageError", args, err)
	}
	return usageErr
}

// Rust's ResumeArgsRaw binds SESSION_ID then PROMPT; a third positional is a
// clap diagnostic, and `--last` turns a lone positional into the prompt.
func TestParseExecResumePositionalsLikeRust(t *testing.T) {
	parsed, err := Parse([]string{"exec", "resume", "session-1", "fix the bug"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Exec.Resume.SessionID != "session-1" || parsed.Exec.Resume.Prompt != "fix the bug" {
		t.Fatalf("resume = %#v", parsed.Exec.Resume)
	}

	parsed, err = Parse([]string{"exec", "resume", "--last", "only prompt"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Exec.Resume.Prompt != "only prompt" || parsed.Exec.Resume.SessionID != "" {
		t.Fatalf("--last resume = %#v", parsed.Exec.Resume)
	}

	parsed, err = Parse([]string{"exec", "resume", "--last", "ignored-id", "the prompt"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Exec.Resume.Prompt != "the prompt" || parsed.Exec.Resume.SessionID != "ignored-id" {
		t.Fatalf("--last two positionals = %#v", parsed.Exec.Resume)
	}

	usageErr := parseUsageError(t, []string{"exec", "resume", "session-1", "a", "b"})
	if usageErr.Message != "unexpected argument 'b' found" || usageErr.Usage != execResumeUsageText {
		t.Fatalf("resume overflow = %q / %q", usageErr.Message, usageErr.Usage)
	}

	usageErr = parseUsageError(t, []string{"exec", "resume", "--all", "session-1", "a", "b"})
	if usageErr.Usage != execResumeUsageText {
		t.Fatalf("--all overflow usage = %q, want the plain page", usageErr.Usage)
	}
}

// clap renders supplied positionals with angle brackets in the flag diagnostic.
func TestExecResumeFlagUsageTextLikeRust(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"exec", "resume", "--bogus"}, "Usage: codex exec resume [OPTIONS] [SESSION_ID] [PROMPT]"},
		{[]string{"exec", "resume", "id", "--bogus"}, "Usage: codex exec resume <SESSION_ID> [PROMPT]"},
		{[]string{"exec", "resume", "id", "a", "--bogus"}, "Usage: codex exec resume <SESSION_ID> <PROMPT>"},
		{[]string{"exec", "resume", "--last", "--bogus"}, "Usage: codex exec resume --last [SESSION_ID] [PROMPT]"},
		{[]string{"exec", "resume", "--last", "a", "--bogus"}, "Usage: codex exec resume --last <SESSION_ID> [PROMPT]"},
		{[]string{"exec", "resume", "--last", "a", "b", "--bogus"}, "Usage: codex exec resume --last <SESSION_ID> <PROMPT>"},
		{[]string{"exec", "resume", "--all", "id", "--bogus"}, "Usage: codex exec resume --all <SESSION_ID> [PROMPT]"},
	}
	for _, testCase := range cases {
		usageErr := parseUsageError(t, testCase.args)
		if usageErr.Usage != testCase.want {
			t.Fatalf("Parse(%v) usage = %q, want %q", testCase.args, usageErr.Usage, testCase.want)
		}
	}
}

func TestParseExecForkAndReviewSinglePromptPositionalLikeRust(t *testing.T) {
	usageErr := parseUsageError(t, []string{"exec", "fork", "id", "a", "b"})
	if usageErr.Message != "unexpected argument 'b' found" || usageErr.Usage != execForkUsageText {
		t.Fatalf("fork overflow = %q / %q", usageErr.Message, usageErr.Usage)
	}

	usageErr = parseUsageError(t, []string{"exec", "review", "a", "b"})
	if usageErr.Message != "unexpected argument 'b' found" || usageErr.Usage != execReviewUsageText {
		t.Fatalf("review overflow = %q / %q", usageErr.Message, usageErr.Usage)
	}

	parsed, err := Parse([]string{"exec", "fork", "id", "continue on the fork"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Exec.Fork.SessionID != "id" || parsed.Exec.Fork.Prompt != "continue on the fork" {
		t.Fatalf("fork = %#v", parsed.Exec.Fork)
	}
}
