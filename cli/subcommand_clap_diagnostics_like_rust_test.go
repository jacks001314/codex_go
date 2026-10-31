package cli

import (
	"errors"
	"strings"
	"testing"
)

func requireClapUsageError(t *testing.T, args []string) *UsageError {
	t.Helper()
	_, err := Parse(args)
	var usageErr *UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("Parse(%v) error = %v, want a *UsageError", args, err)
	}
	return usageErr
}

// TestSubcommandMissingRequiredArgumentsLikeRust pins clap's "required arguments
// were not provided" diagnostic for every subcommand that declares a required
// positional or flag. clap renders the smart usage line, which drops [OPTIONS]
// unless the requirement is an argument group (codex mcp add).
func TestSubcommandMissingRequiredArgumentsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "mcp get",
			args:    []string{"mcp", "get"},
			message: "the following required arguments were not provided:\n  <NAME>",
			usage:   "Usage: codex mcp get <NAME>",
		},
		{
			name:    "mcp login",
			args:    []string{"mcp", "login"},
			message: "the following required arguments were not provided:\n  <NAME>",
			usage:   "Usage: codex mcp login <NAME>",
		},
		{
			name:    "mcp logout",
			args:    []string{"mcp", "logout"},
			message: "the following required arguments were not provided:\n  <NAME>",
			usage:   "Usage: codex mcp logout <NAME>",
		},
		{
			name:    "mcp remove",
			args:    []string{"mcp", "remove"},
			message: "the following required arguments were not provided:\n  <NAME>",
			usage:   "Usage: codex mcp remove <NAME>",
		},
		{
			name:    "mcp add lists the group before the name",
			args:    []string{"mcp", "add"},
			message: "the following required arguments were not provided:\n  <COMMAND|--url <URL>>\n  <NAME>",
			usage:   "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)",
		},
		{
			name:    "mcp add with name needs the group",
			args:    []string{"mcp", "add", "name"},
			message: "the following required arguments were not provided:\n  <COMMAND|--url <URL>>",
			usage:   "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)",
		},
		{
			name:    "mcp add with url needs the name",
			args:    []string{"mcp", "add", "--url", "http://example.test"},
			message: "the following required arguments were not provided:\n  <NAME>",
			usage:   "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)",
		},
		{
			name:    "mcp add after double dash keeps the name",
			args:    []string{"mcp", "add", "--", "--bogus"},
			message: "the following required arguments were not provided:\n  <COMMAND|--url <URL>>",
			usage:   "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)",
		},
		{
			name:    "plugin marketplace add",
			args:    []string{"plugin", "marketplace", "add"},
			message: "the following required arguments were not provided:\n  <SOURCE>",
			usage:   "Usage: codex plugin marketplace add <SOURCE>",
		},
		{
			name:    "plugin marketplace remove",
			args:    []string{"plugin", "marketplace", "remove"},
			message: "the following required arguments were not provided:\n  <MARKETPLACE_NAME>",
			usage:   "Usage: codex plugin marketplace remove <MARKETPLACE_NAME>",
		},
		{
			name:    "apply",
			args:    []string{"apply"},
			message: "the following required arguments were not provided:\n  <TASK_ID>",
			usage:   "Usage: codex apply <TASK_ID>",
		},
		{
			name:    "apply echoes the supplied config flag",
			args:    []string{"apply", "--config", "a=b"},
			message: "the following required arguments were not provided:\n  <TASK_ID>",
			usage:   "Usage: codex apply --config <key=value> <TASK_ID>",
		},
		{
			name:    "delete",
			args:    []string{"delete"},
			message: "the following required arguments were not provided:\n  <SESSION>",
			usage:   "Usage: codex delete <SESSION>",
		},
		{
			name:    "archive",
			args:    []string{"archive"},
			message: "the following required arguments were not provided:\n  <SESSION>",
			usage:   "Usage: codex archive <SESSION>",
		},
		{
			name:    "unarchive",
			args:    []string{"unarchive"},
			message: "the following required arguments were not provided:\n  <SESSION>",
			usage:   "Usage: codex unarchive <SESSION>",
		},
		{
			name:    "debug trace-reduce",
			args:    []string{"debug", "trace-reduce"},
			message: "the following required arguments were not provided:\n  <TRACE_BUNDLE>",
			usage:   "Usage: codex debug trace-reduce <TRACE_BUNDLE>",
		},
		{
			name:    "features enable",
			args:    []string{"features", "enable"},
			message: "the following required arguments were not provided:\n  <FEATURE>",
			usage:   "Usage: codex features enable <FEATURE>",
		},
		{
			name:    "features disable",
			args:    []string{"features", "disable"},
			message: "the following required arguments were not provided:\n  <FEATURE>",
			usage:   "Usage: codex features disable <FEATURE>",
		},
		{
			name:    "queue needs both flags",
			args:    []string{"queue"},
			message: "the following required arguments were not provided:\n  --thread <THREAD>\n  --message <TEXT>",
			usage:   "Usage: codex queue --thread <THREAD> --message <TEXT>",
		},
		{
			name:    "queue with thread still needs the message",
			args:    []string{"queue", "--thread", "t"},
			message: "the following required arguments were not provided:\n  --message <TEXT>",
			usage:   "Usage: codex queue --thread <THREAD> --message <TEXT>",
		},
		{
			name:    "queue with message still needs the thread",
			args:    []string{"queue", "--message", "m"},
			message: "the following required arguments were not provided:\n  --thread <THREAD>",
			usage:   "Usage: codex queue --thread <THREAD> --message <TEXT>",
		},
		{
			name:    "cloud exec",
			args:    []string{"cloud", "exec"},
			message: "the following required arguments were not provided:\n  --env <ENV_ID>",
			usage:   "Usage: codex cloud exec --env <ENV_ID> [QUERY]",
		},
		{
			name:    "cloud status",
			args:    []string{"cloud", "status"},
			message: "the following required arguments were not provided:\n  <TASK_ID>",
			usage:   "Usage: codex cloud status <TASK_ID>",
		},
		{
			name:    "cloud apply",
			args:    []string{"cloud", "apply"},
			message: "the following required arguments were not provided:\n  <TASK_ID>",
			usage:   "Usage: codex cloud apply <TASK_ID>",
		},
		{
			name:    "cloud diff",
			args:    []string{"cloud", "diff"},
			message: "the following required arguments were not provided:\n  <TASK_ID>",
			usage:   "Usage: codex cloud diff <TASK_ID>",
		},
		{
			name:    "exec fork",
			args:    []string{"exec", "fork"},
			message: "the following required arguments were not provided:\n  <SESSION_ID>",
			usage:   "Usage: codex exec fork <SESSION_ID> [PROMPT]",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			usageErr := requireClapUsageError(t, testCase.args)
			if usageErr.Message != testCase.message {
				t.Fatalf("Parse(%v) message = %q, want %q", testCase.args, usageErr.Message, testCase.message)
			}
			if usageErr.Usage != testCase.usage {
				t.Fatalf("Parse(%v) usage = %q, want %q", testCase.args, usageErr.Usage, testCase.usage)
			}
		})
	}
}

// TestSubcommandUnexpectedArgumentsLikeRust pins clap's unexpected-argument
// diagnostic for subcommands: the `--` workaround tip only appears when the
// command declares a positional, and `--flag=value` is echoed without the value.
func TestSubcommandUnexpectedArgumentsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "mcp add unknown flag keeps the tip",
			args:    []string{"mcp", "add", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)",
		},
		{
			name:    "mcp add unknown flag drops the value",
			args:    []string{"mcp", "add", "--bogus=1"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)",
		},
		{
			name:    "mcp add unknown short flag",
			args:    []string{"mcp", "add", "-x"},
			message: "unexpected argument '-x' found\n\n  tip: to pass '-x' as a value, use '-- -x'",
			usage:   "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)",
		},
		{
			name:    "features list second positional has no tip",
			args:    []string{"features", "list", "foo"},
			message: "unexpected argument 'foo' found",
			usage:   "Usage: codex features list [OPTIONS]",
		},
		{
			name:    "features enable second positional has no tip",
			args:    []string{"features", "enable", "a", "b"},
			message: "unexpected argument 'b' found",
			usage:   "Usage: codex features enable [OPTIONS] <FEATURE>",
		},
		{
			name:    "features enable unknown flag keeps the tip",
			args:    []string{"features", "enable", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex features enable [OPTIONS] <FEATURE>",
		},
		{
			name:    "hidden pid-update-loop command has no tip",
			args:    []string{"app-server", "daemon", "pid-update-loop", "--bogus=1"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex app-server daemon pid-update-loop [OPTIONS]",
		},
		{
			name:    "group command unknown flag",
			args:    []string{"mcp", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex mcp [OPTIONS] <COMMAND>",
		},
		{
			name:    "cloud group command unknown flag",
			args:    []string{"cloud", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex cloud [OPTIONS] [COMMAND]",
		},
		{
			name:    "app-server daemon unknown flag",
			args:    []string{"app-server", "daemon", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex app-server daemon [OPTIONS] <COMMAND>",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			usageErr := requireClapUsageError(t, testCase.args)
			if usageErr.Message != testCase.message {
				t.Fatalf("Parse(%v) message = %q, want %q", testCase.args, usageErr.Message, testCase.message)
			}
			if usageErr.Usage != testCase.usage {
				t.Fatalf("Parse(%v) usage = %q, want %q", testCase.args, usageErr.Usage, testCase.usage)
			}
		})
	}
}

// TestSubcommandDidYouMeanLikeRust pins clap's Jaro-based subcommand suggestion,
// including its 0.7 cutoff and the plural wording when two candidates qualify.
func TestSubcommandDidYouMeanLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "mcp suggests one subcommand",
			args:    []string{"mcp", "bogus"},
			message: "unrecognized subcommand 'bogus'\n\n  tip: a similar subcommand exists: 'logout'",
			usage:   "Usage: codex mcp [OPTIONS] <COMMAND>",
		},
		{
			name:    "mcp lists several similar subcommands least similar first",
			args:    []string{"mcp", "logot"},
			message: "unrecognized subcommand 'logot'\n\n  tip: some similar subcommands exist: 'login', 'logout'",
			usage:   "Usage: codex mcp [OPTIONS] <COMMAND>",
		},
		{
			name:    "login suggests status",
			args:    []string{"login", "stat"},
			message: "unrecognized subcommand 'stat'\n\n  tip: a similar subcommand exists: 'status'",
			usage:   "Usage: codex login [OPTIONS] [COMMAND]",
		},
		{
			name:    "login without a close match has no tip",
			args:    []string{"login", "foo"},
			message: "unrecognized subcommand 'foo'",
			usage:   "Usage: codex login [OPTIONS] [COMMAND]",
		},
		{
			name:    "remote-control suggests start",
			args:    []string{"remote-control", "strat"},
			message: "unrecognized subcommand 'strat'\n\n  tip: a similar subcommand exists: 'start'",
			usage:   "Usage: codex remote-control [OPTIONS] [COMMAND]",
		},
		{
			name:    "remote-control without a close match has no tip",
			args:    []string{"remote-control", "serve"},
			message: "unrecognized subcommand 'serve'",
			usage:   "Usage: codex remote-control [OPTIONS] [COMMAND]",
		},
		{
			name:    "features without a close match has no tip",
			args:    []string{"features", "bogus"},
			message: "unrecognized subcommand 'bogus'",
			usage:   "Usage: codex features [OPTIONS] <COMMAND>",
		},
		{
			name:    "plugin marketplace without a close match has no tip",
			args:    []string{"plugin", "marketplace", "bogus"},
			message: "unrecognized subcommand 'bogus'",
			usage:   "Usage: codex plugin marketplace [OPTIONS] <COMMAND>",
		},
		{
			name:    "app-server without a close match has no tip",
			args:    []string{"app-server", "bogus"},
			message: "unrecognized subcommand 'bogus'",
			usage:   "Usage: codex app-server [OPTIONS] [COMMAND]",
		},
		{
			name:    "debug without a close match has no tip",
			args:    []string{"debug", "bogus"},
			message: "unrecognized subcommand 'bogus'",
			usage:   "Usage: codex debug [OPTIONS] <COMMAND>",
		},
		{
			name:    "cloud without a close match has no tip",
			args:    []string{"cloud", "bogus"},
			message: "unrecognized subcommand 'bogus'",
			usage:   "Usage: codex cloud [OPTIONS] [COMMAND]",
		},
		{
			name:    "execpolicy without a close match has no tip",
			args:    []string{"execpolicy", "bogus"},
			message: "unrecognized subcommand 'bogus'",
			usage:   "Usage: codex execpolicy [OPTIONS] <COMMAND>",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			usageErr := requireClapUsageError(t, testCase.args)
			if usageErr.Message != testCase.message {
				t.Fatalf("Parse(%v) message = %q, want %q", testCase.args, usageErr.Message, testCase.message)
			}
			if usageErr.Usage != testCase.usage {
				t.Fatalf("Parse(%v) usage = %q, want %q", testCase.args, usageErr.Usage, testCase.usage)
			}
		})
	}
}

// TestSandboxSetupClapDiagnosticsLikeRust pins the nested `setup` parser: Rust
// wraps clap's error in an anyhow error, so the message keeps clap's own
// "error: " prefix, names the `setup` program and ends with a trailing newline
// that the CLI's `Error: ` line turns into a blank line.
func TestSandboxSetupClapDiagnosticsLikeRust(t *testing.T) {
	const hint = "\n\nFor more information, try '--help'.\n"
	cases := []struct {
		name    string
		args    []string
		message string
	}{
		{
			name:    "unknown flag",
			args:    []string{"sandbox", "setup", "--bogus"},
			message: "error: unexpected argument '--bogus' found\n\nUsage: setup [OPTIONS] <--user <USER>|--current-user>" + hint,
		},
		{
			name:    "unexpected positional",
			args:    []string{"sandbox", "setup", "foo"},
			message: "error: unexpected argument 'foo' found\n\nUsage: setup [OPTIONS] <--user <USER>|--current-user>" + hint,
		},
		{
			name:    "unknown flag echoes the supplied option",
			args:    []string{"sandbox", "setup", "--elevated", "--bogus"},
			message: "error: unexpected argument '--bogus' found\n\nUsage: setup --elevated <--user <USER>|--current-user>" + hint,
		},
		{
			name:    "missing user group",
			args:    []string{"sandbox", "setup"},
			message: "error: the following required arguments were not provided:\n  <--user <USER>|--current-user>\n\nUsage: setup <--user <USER>|--current-user>" + hint,
		},
		{
			name:    "missing user group after elevated",
			args:    []string{"sandbox", "setup", "--elevated"},
			message: "error: the following required arguments were not provided:\n  <--user <USER>|--current-user>\n\nUsage: setup --elevated <--user <USER>|--current-user>" + hint,
		},
		{
			name:    "missing codex home for user",
			args:    []string{"sandbox", "setup", "--user", "bob"},
			message: "error: the following required arguments were not provided:\n  --codex-home <DIR>\n\nUsage: setup --codex-home <DIR> <--user <USER>|--current-user>" + hint,
		},
		{
			name:    "missing user value",
			args:    []string{"sandbox", "setup", "--user"},
			message: "error: a value is required for '--user <USER>' but none was supplied" + hint,
		},
		{
			name:    "missing codex home value",
			args:    []string{"sandbox", "setup", "--codex-home"},
			message: "error: a value is required for '--codex-home <DIR>' but none was supplied" + hint,
		},
		{
			name:    "user conflicts with current user",
			args:    []string{"sandbox", "setup", "--user", "a", "--current-user"},
			message: "error: the argument '--user <USER>' cannot be used with '--current-user'\n\nUsage: setup --codex-home <DIR> <--user <USER>|--current-user>" + hint,
		},
		{
			name:    "current user conflicts with user",
			args:    []string{"sandbox", "setup", "--current-user", "--user", "a"},
			message: "error: the argument '--current-user' cannot be used with '--user <USER>'\n\nUsage: setup <--user <USER>|--current-user>" + hint,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Parse(testCase.args)
			if err == nil {
				t.Fatalf("Parse(%v) error = nil, want the nested setup diagnostic", testCase.args)
			}
			if err.Error() != testCase.message {
				t.Fatalf("Parse(%v) error = %q, want %q", testCase.args, err.Error(), testCase.message)
			}
		})
	}
}

// TestSubcommandMissingValuePlaceholdersLikeRust pins clap's missing-value
// diagnostic for subcommand flags, whose <PLACEHOLDER> differs from the root
// table and sometimes even between commands that share a flag spelling.
func TestSubcommandMissingValuePlaceholdersLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
	}{
		{"queue thread", []string{"queue", "--thread"}, "a value is required for '--thread <THREAD>' but none was supplied"},
		{"queue message", []string{"queue", "--message"}, "a value is required for '--message <TEXT>' but none was supplied"},
		{"mcp add url", []string{"mcp", "add", "n", "--url"}, "a value is required for '--url <URL>' but none was supplied"},
		{"mcp add env", []string{"mcp", "add", "n", "--env"}, "a value is required for '--env <KEY=VALUE>' but none was supplied"},
		{"mcp add bearer token env var", []string{"mcp", "add", "n", "--bearer-token-env-var"}, "a value is required for '--bearer-token-env-var <ENV_VAR>' but none was supplied"},
		{"mcp add oauth client id", []string{"mcp", "add", "n", "--oauth-client-id"}, "a value is required for '--oauth-client-id <CLIENT_ID>' but none was supplied"},
		{"mcp add oauth client secret", []string{"mcp", "add", "n", "--oauth-client-secret"}, "a value is required for '--oauth-client-secret <CLIENT_SECRET>' but none was supplied"},
		{"mcp add oauth resource", []string{"mcp", "add", "n", "--oauth-resource"}, "a value is required for '--oauth-resource <RESOURCE>' but none was supplied"},
		{"cloud exec attempts", []string{"cloud", "exec", "--attempts"}, "a value is required for '--attempts <ATTEMPTS>' but none was supplied"},
		{"cloud exec branch", []string{"cloud", "exec", "--branch"}, "a value is required for '--branch <BRANCH>' but none was supplied"},
		{"cloud list limit", []string{"cloud", "list", "--limit"}, "a value is required for '--limit <N>' but none was supplied"},
		{"cloud list cursor", []string{"cloud", "list", "--cursor"}, "a value is required for '--cursor <CURSOR>' but none was supplied"},
		{"cloud apply attempt", []string{"cloud", "apply", "--attempt"}, "a value is required for '--attempt <N>' but none was supplied"},
		{"execpolicy rules", []string{"execpolicy", "check", "--rules"}, "a value is required for '--rules <PATH>' but none was supplied"},
		{"app-server proxy sock", []string{"app-server", "proxy", "--sock"}, "a value is required for '--sock <SOCKET_PATH>' but none was supplied"},
		{"generate-ts out", []string{"app-server", "generate-ts", "--out"}, "a value is required for '--out <DIR>' but none was supplied"},
		{"sandbox profile", []string{"sandbox", "--profile"}, "a value is required for '--profile <CONFIG_PROFILE>' but none was supplied"},
		{"exec-server remote", []string{"exec-server", "--remote"}, "a value is required for '--remote <URL>' but none was supplied"},
		{"migrate-rollouts thread", []string{"migrate-rollouts", "--thread"}, "a value is required for '--thread <THREAD_ID>' but none was supplied"},
		{"pid-update-loop restore release", []string{"app-server", "daemon", "pid-update-loop", "--restore-release"}, "a value is required for '--restore-release <RESTORE_RELEASE>' but none was supplied"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			usageErr := requireClapUsageError(t, testCase.args)
			if usageErr.Message != testCase.message {
				t.Fatalf("Parse(%v) message = %q, want %q", testCase.args, usageErr.Message, testCase.message)
			}
			if usageErr.Usage != "" {
				t.Fatalf("Parse(%v) usage = %q, want no usage block", testCase.args, usageErr.Usage)
			}
		})
	}
}

// TestJaroSimilarityMatchesStrsimLikeRust pins the similarity metric clap uses:
// strsim's Jaro scores decide the suggestion list, and the 0.7 cutoff includes
// scores that land a hair above it through floating point rounding.
func TestJaroSimilarityMatchesStrsimLikeRust(t *testing.T) {
	cases := []struct {
		left, right string
		want        float64
	}{
		{"bogus", "logout", 0.7},
		{"logot", "login", 0.7333333333333334},
		{"logot", "logout", 0.9444444444444445},
		{"stat", "status", 0.8888888888888888},
		{"strat", "start", 0.9333333333333332},
	}
	for _, testCase := range cases {
		got := jaroSimilarity(testCase.left, testCase.right)
		if got < testCase.want-1e-12 || got > testCase.want+1e-12 {
			t.Fatalf("jaroSimilarity(%q, %q) = %v, want %v", testCase.left, testCase.right, got, testCase.want)
		}
	}
	if suggestions := suggestedSubcommands("bogus", subcommandCandidates["mcp"]); len(suggestions) != 1 || suggestions[0] != "logout" {
		t.Fatalf("suggestedSubcommands(bogus, mcp) = %v, want [logout]", suggestions)
	}
	if suggestions := suggestedSubcommands("logot", subcommandCandidates["mcp"]); strings.Join(suggestions, ",") != "login,logout" {
		t.Fatalf("suggestedSubcommands(logot, mcp) = %v, want [login logout]", suggestions)
	}
}
