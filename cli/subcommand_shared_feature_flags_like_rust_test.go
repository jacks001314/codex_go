package cli

import (
	"strings"
	"testing"
)

// TestSubcommandSharedFeatureFlagsLikeRust pins clap's flattened
// `--enable`/`--disable` arguments. Rust declares them on every subcommand, so
// `codex login --enable alpha` parses exactly like `codex --enable alpha login`
// and the values reach the shared startup path through RootOptions.
func TestSubcommandSharedFeatureFlagsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		enable  []string
		disable []string
	}{
		{
			name:   "login --enable",
			args:   []string{"login", "--enable", "alpha"},
			enable: []string{"alpha"},
		},
		{
			name:   "logout --enable=",
			args:   []string{"logout", "--enable=alpha"},
			enable: []string{"alpha"},
		},
		{
			name:   "mcp list repeats --enable",
			args:   []string{"mcp", "list", "--enable", "alpha", "--enable", "beta"},
			enable: []string{"alpha", "beta"},
		},
		{
			name:    "mcp list --disable",
			args:    []string{"mcp", "list", "--disable", "gamma"},
			disable: []string{"gamma"},
		},
		{
			name:    "features list --disable=",
			args:    []string{"features", "list", "--disable=gamma"},
			disable: []string{"gamma"},
		},
		{
			name:   "update --enable",
			args:   []string{"update", "--enable", "alpha"},
			enable: []string{"alpha"},
		},
		{
			name:   "sandbox uninstall --enable",
			args:   []string{"sandbox", "uninstall", "--enable", "alpha"},
			enable: []string{"alpha"},
		},
		{
			name:   "enable and disable mix in parse order",
			args:   []string{"agents", "--disable", "gamma", "--enable", "alpha"},
			enable: []string{"alpha"}, disable: []string{"gamma"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := Parse(testCase.args)
			if err != nil {
				t.Fatalf("Parse(%v) error = %v", testCase.args, err)
			}
			if got := strings.Join(parsed.Root.EnableFeatures, ","); got != strings.Join(testCase.enable, ",") {
				t.Fatalf("Parse(%v) enable = %q, want %q", testCase.args, got, strings.Join(testCase.enable, ","))
			}
			if got := strings.Join(parsed.Root.DisableFeatures, ","); got != strings.Join(testCase.disable, ",") {
				t.Fatalf("Parse(%v) disable = %q, want %q", testCase.args, got, strings.Join(testCase.disable, ","))
			}
		})
	}
}

// TestSubcommandSharedFeatureFlagMissingValueLikeRust pins clap's pending-value
// diagnostic: the flags never take a leading-dash token as their value and clap
// prints the message without a usage block.
func TestSubcommandSharedFeatureFlagMissingValueLikeRust(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "enable at end of argv",
			args: []string{"login", "--enable"},
			want: "a value is required for '--enable <FEATURE>' but none was supplied",
		},
		{
			name: "disable at end of argv",
			args: []string{"login", "--disable"},
			want: "a value is required for '--disable <FEATURE>' but none was supplied",
		},
		{
			name: "enable followed by another flag",
			args: []string{"login", "--enable", "--config", "a=b"},
			want: "a value is required for '--enable <FEATURE>' but none was supplied",
		},
		{
			name: "enable followed by a command flag",
			args: []string{"mcp", "list", "--enable", "--json"},
			want: "a value is required for '--enable <FEATURE>' but none was supplied",
		},
		{
			name: "update disable at end of argv",
			args: []string{"update", "--disable"},
			want: "a value is required for '--disable <FEATURE>' but none was supplied",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			usageErr := requireClapUsageError(t, testCase.args)
			if usageErr.Message != testCase.want {
				t.Fatalf("Parse(%v) message = %q, want %q", testCase.args, usageErr.Message, testCase.want)
			}
			if usageErr.Usage != "" {
				t.Fatalf("Parse(%v) usage = %q, want no usage block", testCase.args, usageErr.Usage)
			}
		})
	}
}

// TestSandboxSetupRejectsSharedFeatureFlagsLikeRust keeps the one exception:
// Rust parses `sandbox setup` with its own clap definition, so the shared
// flags are unknown there.
func TestSandboxSetupRejectsSharedFeatureFlagsLikeRust(t *testing.T) {
	_, err := Parse([]string{"sandbox", "setup", "--enable", "alpha"})
	if err == nil || !strings.Contains(err.Error(), "unexpected argument '--enable' found") {
		t.Fatalf("Parse(sandbox setup --enable) error = %v, want the setup clap diagnostic", err)
	}
}

// TestSplitSubcommandFeatureFlagsKeepsSeparator documents the `--` rule: every
// token after the separator is positional for the subcommand.
func TestSplitSubcommandFeatureFlagsKeepsSeparator(t *testing.T) {
	parsed := &Parsed{Command: CommandSandbox}
	rest, err := parsed.splitSubcommandFeatureFlags([]string{"--enable", "alpha", "--", "--enable", "beta"})
	if err != nil {
		t.Fatalf("split error = %v", err)
	}
	if got := strings.Join(rest, " "); got != "-- --enable beta" {
		t.Fatalf("remaining args = %q, want %q", got, "-- --enable beta")
	}
	if got := strings.Join(parsed.Root.EnableFeatures, ","); got != "alpha" {
		t.Fatalf("enable = %q, want alpha", got)
	}
}

// TestSubcommandSmartUsageEchoesUsedOptionsLikeRust pins clap's smart usage:
// as soon as clap consumed an option the usage block drops [OPTIONS] and echoes
// the options it saw, in argv order, in front of the positionals.
func TestSubcommandSmartUsageEchoesUsedOptionsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "mcp get echoes the shared flag and json",
			args:    []string{"mcp", "get", "--enable", "alpha", "--json"},
			message: "the following required arguments were not provided:\n  <NAME>",
			usage:   "Usage: codex mcp get --enable <FEATURE> --json <NAME>",
		},
		{
			name:    "mcp get echoes json alone",
			args:    []string{"mcp", "get", "--json"},
			message: "the following required arguments were not provided:\n  <NAME>",
			usage:   "Usage: codex mcp get --json <NAME>",
		},
		{
			name:    "delete echoes force and the shared flag",
			args:    []string{"delete", "--force", "--enable", "alpha"},
			message: "the following required arguments were not provided:\n  <SESSION>",
			usage:   "Usage: codex delete --force --enable <FEATURE> <SESSION>",
		},
		{
			name:    "unarchive echoes disable",
			args:    []string{"unarchive", "--disable", "alpha"},
			message: "the following required arguments were not provided:\n  <SESSION>",
			usage:   "Usage: codex unarchive --disable <FEATURE> <SESSION>",
		},
		{
			name:    "apply echoes disable",
			args:    []string{"apply", "--disable", "alpha"},
			message: "the following required arguments were not provided:\n  <TASK_ID>",
			usage:   "Usage: codex apply --disable <FEATURE> <TASK_ID>",
		},
		{
			name:    "queue appends the shared flag after the required options",
			args:    []string{"queue", "--enable", "alpha"},
			message: "the following required arguments were not provided:\n  --thread <THREAD>\n  --message <TEXT>",
			usage:   "Usage: codex queue --thread <THREAD> --message <TEXT> --enable <FEATURE>",
		},
		{
			name:    "queue keeps one copy of an already supplied required option",
			args:    []string{"queue", "--thread", "t"},
			message: "the following required arguments were not provided:\n  --message <TEXT>",
			usage:   "Usage: codex queue --thread <THREAD> --message <TEXT>",
		},
		{
			name:    "mcp add keeps the optional block for its argument group",
			args:    []string{"mcp", "add", "--enable", "alpha"},
			message: "the following required arguments were not provided:\n  <COMMAND|--url <URL>>\n  <NAME>",
			usage:   "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)",
		},
		{
			name:    "features list echoes the shared flag for an unknown argument",
			args:    []string{"features", "list", "--enable", "alpha", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex features list --enable <FEATURE>",
		},
		{
			name:    "agents echoes the shared flag for an unknown argument",
			args:    []string{"agents", "--enable", "alpha", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex agents --enable <FEATURE>",
		},
		{
			name:    "mcp get keeps the optional block when nothing was consumed",
			args:    []string{"mcp", "get", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex mcp get [OPTIONS] <NAME>",
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

// TestConfigOptionRefusesFlagLikeValueLikeRust pins clap's value rule for the
// shared `-c`/`--config` option, which used to swallow the next flag.
func TestConfigOptionRefusesFlagLikeValueLikeRust(t *testing.T) {
	usageErr := requireClapUsageError(t, []string{"mcp", "get", "-c", "--json"})
	if usageErr.Message != "a value is required for '--config <key=value>' but none was supplied" {
		t.Fatalf("mcp get -c --json message = %q", usageErr.Message)
	}
	if usageErr.Usage != "" {
		t.Fatalf("mcp get -c --json usage = %q, want no usage block", usageErr.Usage)
	}
}
