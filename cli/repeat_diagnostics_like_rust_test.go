package cli

import "testing"

// TestRepeatedOptionDiagnosticsLikeRust pins clap's occurrence checks across the
// command tree: a SetTrue flag fails while argv is matched, a single-value
// option fails in the post-parse validator, both print the static usage page
// and the repeatable actions (config/enable/disable/add-dir/image plus the
// per-command env/scopes/sparse lists) accept a second occurrence.
func TestRepeatedOptionDiagnosticsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "root --strict-config",
			args:    []string{"--strict-config", "--strict-config", "--bogus"},
			message: "the argument '--strict-config' cannot be used multiple times",
			usage:   "Usage: codex [OPTIONS] [PROMPT]\n       codex [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "root --oss",
			args:    []string{"--oss", "--oss", "--bogus"},
			message: "the argument '--oss' cannot be used multiple times",
			usage:   "Usage: codex [OPTIONS] [PROMPT]\n       codex [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "root --yolo",
			args:    []string{"--yolo", "--yolo", "--bogus"},
			message: "the argument '--dangerously-bypass-approvals-and-sandbox' cannot be used multiple times",
			usage:   "Usage: codex [OPTIONS] [PROMPT]\n       codex [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "root --no-alt-screen",
			args:    []string{"--no-alt-screen", "--no-alt-screen", "--bogus"},
			message: "the argument '--no-alt-screen' cannot be used multiple times",
			usage:   "Usage: codex [OPTIONS] [PROMPT]\n       codex [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "exec --json",
			args:    []string{"exec", "--json", "--json", "--bogus"},
			message: "the argument '--json' cannot be used multiple times",
			usage:   "Usage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "exec --skip-git-repo-check",
			args:    []string{"exec", "--skip-git-repo-check", "--skip-git-repo-check", "--bogus"},
			message: "the argument '--skip-git-repo-check' cannot be used multiple times",
			usage:   "Usage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "exec --ephemeral",
			args:    []string{"exec", "--ephemeral", "--ephemeral", "--bogus"},
			message: "the argument '--ephemeral' cannot be used multiple times",
			usage:   "Usage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "agents --no-alt-screen",
			args:    []string{"agents", "--no-alt-screen", "--no-alt-screen", "--bogus"},
			message: "the argument '--no-alt-screen' cannot be used multiple times",
			usage:   "Usage: codex agents [OPTIONS]",
		},
		{
			name:    "doctor --summary",
			args:    []string{"doctor", "--summary", "--summary", "--bogus"},
			message: "the argument '--summary' cannot be used multiple times",
			usage:   "Usage: codex doctor [OPTIONS]",
		},
		{
			name:    "doctor --json",
			args:    []string{"doctor", "--json", "--json", "--bogus"},
			message: "the argument '--json' cannot be used multiple times",
			usage:   "Usage: codex doctor [OPTIONS]",
		},
		{
			name:    "queue --resume",
			args:    []string{"queue", "--resume", "--resume", "--bogus"},
			message: "the argument '--resume' cannot be used multiple times",
			usage:   "Usage: codex queue [OPTIONS] --thread <THREAD> --message <TEXT>",
		},
		{
			name:    "app-server --strict-config",
			args:    []string{"app-server", "--strict-config", "--strict-config", "--bogus"},
			message: "the argument '--strict-config' cannot be used multiple times",
			usage:   "Usage: codex app-server [OPTIONS] [COMMAND]",
		},
		{
			name:    "mcp add --oauth-client-registration",
			args:    []string{"mcp", "add", "--oauth-client-registration", "--oauth-client-registration", "--bogus"},
			message: "a value is required for '--oauth-client-registration <AUTO|CIMD|DCR>' but none was supplied\n  [possible values: auto, cimd, dcr]",
			usage:   "",
		},
		{
			name:    "sandbox --sandbox-state-disable-network",
			args:    []string{"sandbox", "--sandbox-state-disable-network", "--sandbox-state-disable-network", "--bogus"},
			message: "the argument '--sandbox-state-disable-network' cannot be used multiple times",
			usage:   "Usage: codex sandbox [OPTIONS] [COMMAND]... [COMMAND]",
		},
		{
			name:    "migrate-rollouts --verbose",
			args:    []string{"migrate-rollouts", "--verbose", "--verbose", "--bogus"},
			message: "the argument '--verbose' cannot be used multiple times",
			usage:   "Usage: codex migrate-rollouts [OPTIONS]",
		},
		{
			name:    "exec-server forward --strict-config",
			args:    []string{"exec-server", "forward", "--strict-config", "--strict-config", "--bogus"},
			message: "the argument '--strict-config' cannot be used multiple times",
			usage:   "Usage: codex exec-server forward [OPTIONS] --remote <URL> --environment-id <ID> --connect <URL>",
		},
		{
			name:    "plugin marketplace add --json",
			args:    []string{"plugin", "marketplace", "add", "--json", "--json", "--bogus"},
			message: "the argument '--json' cannot be used multiple times",
			usage:   "Usage: codex plugin marketplace add [OPTIONS] <SOURCE>",
		},
		{
			name:    "exec --model",
			args:    []string{"exec", "--model", "gpt-5", "--model", "gpt-5"},
			message: "the argument '--model <MODEL>' cannot be used multiple times",
			usage:   "Usage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "exec --cd",
			args:    []string{"exec", "--cd", ".", "--cd", "."},
			message: "the argument '--cd <DIR>' cannot be used multiple times",
			usage:   "Usage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "exec --sandbox",
			args:    []string{"exec", "--sandbox", "read-only", "--sandbox", "read-only"},
			message: "the argument '--sandbox <SANDBOX_MODE>' cannot be used multiple times",
			usage:   "Usage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "exec --profile",
			args:    []string{"exec", "--profile", "p", "--profile", "p"},
			message: "the argument '--profile <CONFIG_PROFILE_V2>' cannot be used multiple times",
			usage:   "Usage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "queue --message",
			args:    []string{"queue", "--message", "m", "--message", "m"},
			message: "the argument '--message <TEXT>' cannot be used multiple times",
			usage:   "Usage: codex queue [OPTIONS] --thread <THREAD> --message <TEXT>",
		},
		{
			name:    "queue --thread",
			args:    []string{"queue", "--thread", "t", "--thread", "t"},
			message: "the argument '--thread <THREAD>' cannot be used multiple times",
			usage:   "Usage: codex queue [OPTIONS] --thread <THREAD> --message <TEXT>",
		},
		{
			name:    "queue --remote",
			args:    []string{"queue", "--remote", "ws://127.0.0.1:1", "--remote", "ws://127.0.0.1:1"},
			message: "the argument '--remote <ADDR>' cannot be used multiple times",
			usage:   "Usage: codex queue [OPTIONS] --thread <THREAD> --message <TEXT>",
		},
		{
			name:    "mcp add --url",
			args:    []string{"mcp", "add", "--url", "http://127.0.0.1:1", "--url", "http://127.0.0.1:1"},
			message: "the argument '--url <URL>' cannot be used multiple times",
			usage:   "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)",
		},
		{
			name:    "sandbox --profile",
			args:    []string{"sandbox", "--profile", "p", "--profile", "p"},
			message: "the argument '--profile <CONFIG_PROFILE>' cannot be used multiple times",
			usage:   "Usage: codex sandbox [OPTIONS] [COMMAND]... [COMMAND]",
		},
		{
			name:    "app-server --listen",
			args:    []string{"app-server", "--listen", "stdio://", "--listen", "stdio://"},
			message: "the argument '--listen <URL>' cannot be used multiple times",
			usage:   "Usage: codex app-server [OPTIONS] [COMMAND]",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			usageErr := requireClapUsageError(t, testCase.args)
			if usageErr.Message != testCase.message {
				t.Fatalf("message = %q, want %q", usageErr.Message, testCase.message)
			}
			if usageErr.Usage != testCase.usage {
				t.Fatalf("usage = %q, want %q", usageErr.Usage, testCase.usage)
			}
		})
	}
}

// TestRepeatableOptionsStayAcceptedLikeRust pins the actions clap lets argv
// repeat: the shared overrides, the variadic value options and the
// per-command env/scopes/sparse lists must still parse.
func TestRepeatableOptionsStayAcceptedLikeRust(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"exec image", []string{"exec", "--image", "a", "--image", "b", "--skip-git-repo-check"}},
		{"exec add-dir", []string{"exec", "--add-dir", ".", "--add-dir", ".", "--skip-git-repo-check"}},
		{"exec enable disable", []string{"exec", "--enable", "a", "--enable", "b", "--disable", "c", "--skip-git-repo-check"}},
		{"exec config", []string{"exec", "-c", "a=b", "-c", "c=d", "--skip-git-repo-check"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := Parse(testCase.args); err != nil {
				t.Fatalf("Parse(%v) error = %v, want none", testCase.args, err)
			}
		})
	}
}

// TestExecThreadSourceConsumesItsValueLikeRust pins the arity of
// `codex exec --thread-source <SOURCE>`: the value must not fall through as
// the PROMPT, so a second positional is still a clap diagnostic.
func TestExecThreadSourceConsumesItsValueLikeRust(t *testing.T) {
	parsed, err := Parse([]string{"exec", "--thread-source", "x", "y"})
	if err != nil {
		t.Fatalf("Parse error = %v, want none", err)
	}
	if parsed.Exec.ThreadSource != "x" {
		t.Fatalf("thread source = %q, want x", parsed.Exec.ThreadSource)
	}
	if parsed.Exec.Prompt != "y" {
		t.Fatalf("prompt = %q, want y", parsed.Exec.Prompt)
	}
}
