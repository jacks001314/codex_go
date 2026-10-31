package cli

import (
	"errors"
	"strings"
	"testing"
)

const worktreeSessionID = "019f1234-5678-7000-8000-000000000001"

// TestWorktreeSupportedCommandsLikeRust pins the argument sets Rust's
// reject_unsupported_worktree_for_subcommand accepts: new interactive
// sessions, `codex fork <ID>`, `codex exec` and `codex exec fork`.
func TestWorktreeSupportedCommandsLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"--worktree"},
		{"--worktree", "hello"},
		{"--worktree", "exec", "hello"},
		{"exec", "--worktree", "hello"},
		{"fork", "--worktree", worktreeSessionID},
		{"exec", "fork", "--worktree", worktreeSessionID},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := Parse(args); err != nil {
				t.Fatalf("Parse(%v) error = %v, want no error", args, err)
			}
		})
	}
}

// TestWorktreeRejectedCommandsLikeRust pins Rust's post-parse worktree gate:
// `--worktree` only creates a fresh managed worktree, so session and management
// commands are rejected with dedicated wording.
func TestWorktreeRejectedCommandsLikeRust(t *testing.T) {
	const generic = "`--worktree` supports new interactive sessions, `codex fork`, `codex exec`, and `codex exec fork`"
	cases := []struct {
		args    []string
		message string
	}{
		{[]string{"--worktree", "login"}, generic},
		{[]string{"fork", "--worktree"}, "`codex fork --worktree` requires an explicit session ID"},
		{[]string{"fork", "--worktree", "--last"}, "`codex fork --worktree` requires an explicit session ID"},
		{[]string{"exec", "resume", "--worktree", "session"}, "`--worktree` cannot resume an existing session; use `codex exec fork --worktree`"},
		{[]string{"exec", "review", "--worktree"}, "`--worktree` is not supported for code review"},
		{[]string{"exec", "review", "--worktree", "x"}, "`--worktree` is not supported for code review"},
		{[]string{"exec", "review", "--model", "gpt-5.1-codex", "--worktree"}, "`--worktree` is not supported for code review"},
		{[]string{"exec", "review", "--dangerously-bypass-approvals-and-sandbox", "--worktree"}, "`--worktree` is not supported for code review"},
		{[]string{"exec", "review", "--dangerously-bypass-hook-trust", "--worktree"}, "`--worktree` is not supported for code review"},
		{[]string{"resume", "--worktree", "session"}, generic},
		{[]string{"archive", "session", "--worktree"}, generic},
		{[]string{"unarchive", "session", "--worktree"}, generic},
		{[]string{"delete", "session", "--worktree"}, generic},
		{[]string{"queue", "--thread", "session", "--message", "hi", "--worktree"}, generic},
		{[]string{"--strict-config", "--worktree", "mcp", "list"}, generic},
		{[]string{"--worktree", "--strict-config", "login"}, generic},
	}
	for _, testCase := range cases {
		t.Run(strings.Join(testCase.args, " "), func(t *testing.T) {
			_, err := Parse(testCase.args)
			if err == nil {
				t.Fatalf("Parse(%v) error = nil, want %q", testCase.args, testCase.message)
			}
			if err.Error() != testCase.message {
				t.Fatalf("Parse(%v) error = %q, want %q", testCase.args, err.Error(), testCase.message)
			}
		})
	}
}

// TestExecReviewAcceptsExecGlobalOptionsLikeRust pins Rust's global marking:
// `codex exec review` takes model, the dangerous bypasses and --worktree after
// the subcommand, but not the other shared options.
func TestExecReviewAcceptsExecGlobalOptionsLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"exec", "review", "--uncommitted", "--model", "gpt-5.1-codex"},
		{"exec", "review", "--uncommitted", "--model=gpt-5.1-codex"},
		{"exec", "review", "--uncommitted", "-m", "gpt-5.1-codex"},
		{"exec", "review", "--uncommitted", "--dangerously-bypass-approvals-and-sandbox"},
		{"exec", "review", "--uncommitted", "--dangerously-bypass-hook-trust"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			parsed, err := Parse(args)
			if err != nil {
				t.Fatalf("Parse(%v) error = %v, want no error", args, err)
			}
			if parsed.Exec.Review.Uncommitted != true {
				t.Fatalf("Parse(%v) did not keep --uncommitted", args)
			}
		})
	}
	usageErr := requireClapUsageError(t, []string{"exec", "review", "--sandbox", "read-only"})
	const wantMessage = "unexpected argument '--sandbox' found\n\n  tip: to pass '--sandbox' as a value, use '-- --sandbox'"
	if usageErr.Message != wantMessage {
		t.Fatalf("message = %q, want %q", usageErr.Message, wantMessage)
	}
	if usageErr.Usage != "Usage: codex exec review [OPTIONS] [PROMPT]" {
		t.Fatalf("usage = %q", usageErr.Usage)
	}
}

// TestMcpListRejectsWorktreeLikeRust pins the clap layer: `codex mcp list`
// does not flatten the root shared options, so --worktree never reaches the
// post-parse gate.
func TestMcpListRejectsWorktreeLikeRust(t *testing.T) {
	usageErr := requireClapUsageError(t, []string{"mcp", "list", "--worktree"})
	if usageErr.Message != "unexpected argument '--worktree' found" {
		t.Fatalf("message = %q", usageErr.Message)
	}
	if usageErr.Usage != "Usage: codex mcp list [OPTIONS]" {
		t.Fatalf("usage = %q", usageErr.Usage)
	}
}

// TestWorktreeRunsBeforeSubcommandGatesLikeRust pins the ordering: Rust rejects
// --worktree before the strict-config gate and before exec validates its own
// session selector.
func TestWorktreeRunsBeforeSubcommandGatesLikeRust(t *testing.T) {
	_, err := Parse([]string{"--worktree", "--strict-config", "mcp", "list"})
	if err == nil || strings.Contains(err.Error(), "strict-config") {
		t.Fatalf("Parse error = %v, want the worktree rejection", err)
	}
	_, err = Parse([]string{"exec", "resume", "--worktree"})
	if err == nil || !strings.Contains(err.Error(), "cannot resume an existing session") {
		t.Fatalf("Parse error = %v, want the worktree resume rejection", err)
	}
}

// TestStrictConfigGateLikeRust pins Rust's unsupported_subcommand_name_for_
// strict_config allow-list and the remote-control subcommand names it embeds.
func TestStrictConfigGateLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"--strict-config", "agents"},
		{"--strict-config", "doctor"},
		{"--strict-config", "queue", "--thread", "t", "--message", "m"},
		{"--strict-config", "archive", "abc"},
		{"--strict-config", "app-server"},
	} {
		t.Run("allowed "+strings.Join(args, " "), func(t *testing.T) {
			if _, err := Parse(args); err != nil {
				t.Fatalf("Parse(%v) error = %v, want no error", args, err)
			}
		})
	}
	rejected := []struct {
		args    []string
		message string
	}{
		{[]string{"--strict-config", "mcp", "list"}, "`--strict-config` is not supported for `codex mcp`"},
		{[]string{"--strict-config", "plugin", "list"}, "`--strict-config` is not supported for `codex plugin`"},
		{[]string{"--strict-config", "app-server", "proxy"}, "`--strict-config` is not supported for `codex app-server proxy`"},
		{[]string{"--strict-config", "remote-control"}, "`--strict-config` is not supported for `codex remote-control`"},
		{[]string{"--strict-config", "remote-control", "start"}, "`--strict-config` is not supported for `codex remote-control start`"},
		{[]string{"--strict-config", "remote-control", "pair"}, "`--strict-config` is not supported for `codex remote-control pair`"},
	}
	for _, testCase := range rejected {
		t.Run("rejected "+strings.Join(testCase.args, " "), func(t *testing.T) {
			_, err := Parse(testCase.args)
			if err == nil || err.Error() != testCase.message {
				t.Fatalf("Parse(%v) error = %v, want %q", testCase.args, err, testCase.message)
			}
		})
	}
}

// TestAgentsRejectsStrictConfigAfterSubcommandLikeRust pins Rust's AgentsCommand
// surface: --strict-config is not flattened there, so clap reports it as an
// unexpected argument while the root spelling stays valid.
func TestAgentsRejectsStrictConfigAfterSubcommandLikeRust(t *testing.T) {
	usageErr := requireClapUsageError(t, []string{"agents", "--strict-config"})
	if usageErr.Message != "unexpected argument '--strict-config' found" {
		t.Fatalf("message = %q", usageErr.Message)
	}
	if usageErr.Usage != "Usage: codex agents [OPTIONS]" {
		t.Fatalf("usage = %q", usageErr.Usage)
	}
	var typed *UsageError
	if _, err := Parse([]string{"--strict-config", "agents"}); errors.As(err, &typed) {
		t.Fatalf("root spelling returned a clap error: %v", err)
	}
}
