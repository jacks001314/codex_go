package cli

import (
	"strings"
	"testing"
)

// TestShortFlagDiagnosticsLikeRust pins clap's short-flag diagnostics: an
// unknown short alias is named by its first letter only and always rendered
// against the command's static usage page, because clap resolves short flags
// before it records the options it already accepted.
func TestShortFlagDiagnosticsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "mcp get --json -X",
			args:    []string{"mcp", "get", "--json", "-X"},
			message: "unexpected argument '-X' found\n\n  tip: to pass '-X' as a value, use '-- -X'",
			usage:   "Usage: codex mcp get [OPTIONS] <NAME>",
		},
		{
			name:    "root -X1 names the first letter",
			args:    []string{"-X1"},
			message: "unexpected argument '-X' found\n\n  tip: to pass '-X' as a value, use '-- -X'",
			usage:   rootUsageText,
		},
		{
			name:    "exec resume -m x -X",
			args:    []string{"exec", "resume", "-m", "x", "-X"},
			message: "unexpected argument '-X' found\n\n  tip: to pass '-X' as a value, use '-- -X'",
			usage:   "Usage: codex exec resume [OPTIONS] [SESSION_ID] [PROMPT]",
		},
		{
			name:    "doctor --summary -X",
			args:    []string{"doctor", "--summary", "-X"},
			message: "unexpected argument '-X' found",
			usage:   doctorUsageText,
		},
		{
			name:    "queue --thread t --message m -X",
			args:    []string{"queue", "--thread", "t", "--message", "m", "-X"},
			message: "unexpected argument '-X' found",
			usage:   "Usage: codex queue [OPTIONS] --thread <THREAD> --message <TEXT>",
		},
		{
			name:    "plugin add --json -X src",
			args:    []string{"plugin", "add", "--json", "-X", "src"},
			message: "unexpected argument '-X' found\n\n  tip: to pass '-X' as a value, use '-- -X'",
			usage:   "Usage: codex plugin add [OPTIONS] <PLUGIN[@MARKETPLACE]>",
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

// TestPendingValueResolutionLikeRust pins the order clap resolves a value-less
// option: it diagnoses the following token first when that token is an unknown
// flag, while a declared flag keeps the missing-value message.
func TestPendingValueResolutionLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "exec -c -x1",
			args:    []string{"exec", "-c", "-x1"},
			message: "unexpected argument '-x' found\n\n  tip: to pass '-x' as a value, use '-- -x'",
			usage:   "Usage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "exec -c --bogus",
			args:    []string{"exec", "-c", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "exec -c --json keeps the pending value",
			args:    []string{"exec", "-c", "--json"},
			message: "a value is required for '--config <key=value>' but none was supplied",
			usage:   "",
		},
		{
			name:    "debug models -c -X",
			args:    []string{"debug", "models", "-c", "-X"},
			message: "unexpected argument '-X' found",
			usage:   "Usage: codex debug models [OPTIONS]",
		},
		{
			name:    "debug models -c keeps the pending value",
			args:    []string{"debug", "models", "-c"},
			message: "a value is required for '--config <key=value>' but none was supplied",
			usage:   "",
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

// TestShortFlagUsageSpellingLikeRust pins the long spelling clap echoes for a
// short alias, which depends on the command: `-m` is --model for `exec` but
// --marketplace for `plugin add`, `-o` is --output-last-message for the exec
// family, and `-y` is a boolean --yes.
func TestShortFlagUsageSpellingLikeRust(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		usage string
	}{
		{
			name:  "plugin add -m",
			args:  []string{"plugin", "add", "-m", "mkt", "--bogus", "src"},
			usage: "Usage: codex plugin add --marketplace <MARKETPLACE> <PLUGIN[@MARKETPLACE]>",
		},
		{
			name:  "exec resume -o",
			args:  []string{"exec", "resume", "-o", "f", "--bogus"},
			usage: "Usage: codex exec resume --output-last-message <FILE> [SESSION_ID] [PROMPT]",
		},
		{
			name:  "app-server daemon update -y",
			args:  []string{"app-server", "daemon", "update", "-y", "--bogus"},
			usage: "Usage: codex app-server daemon update --yes",
		},
		{
			name:  "app-server generate-ts -o and -p",
			args:  []string{"app-server", "generate-ts", "-o", "dir", "-p", "bin", "--bogus"},
			usage: "Usage: codex app-server generate-ts --out <DIR> --prettier <PRETTIER_BIN>",
		},
		{
			name:  "sandbox -p keeps its own placeholder",
			args:  []string{"sandbox", "-p", "p", "--bogus"},
			usage: "Usage: codex sandbox --profile <CONFIG_PROFILE> [COMMAND]...",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			usageErr := requireClapUsageError(t, testCase.args)
			if usageErr.Usage != testCase.usage {
				t.Fatalf("usage = %q, want %q", usageErr.Usage, testCase.usage)
			}
		})
	}
}

// TestSubcommandConfigOverrideAcceptanceLikeRust pins clap's global
// `-c`/`--config`: every leaf accepts it, `--` ends option parsing so a
// following token can be the positional, and an overflowing positional is
// reported against the static page.
func TestSubcommandConfigOverrideAcceptanceLikeRust(t *testing.T) {
	t.Run("mcp get -- --json names the server", func(t *testing.T) {
		parsed, err := Parse([]string{"mcp", "get", "--", "--json"})
		if err != nil {
			t.Fatalf("Parse error = %v", err)
		}
		if parsed.MCP.Name != "--json" {
			t.Fatalf("name = %q, want %q", parsed.MCP.Name, "--json")
		}
		if parsed.MCP.JSON {
			t.Fatal("--json after the separator must not set the flag")
		}
	})
	t.Run("mcp get -- a b", func(t *testing.T) {
		usageErr := requireClapUsageError(t, []string{"mcp", "get", "--", "a", "b"})
		if usageErr.Message != "unexpected argument 'b' found" {
			t.Fatalf("message = %q", usageErr.Message)
		}
		if usageErr.Usage != "Usage: codex mcp get [OPTIONS] <NAME>" {
			t.Fatalf("usage = %q", usageErr.Usage)
		}
	})
	t.Run("doctor -c a=b --bogus echoes the override", func(t *testing.T) {
		usageErr := requireClapUsageError(t, []string{"doctor", "-c", "a=b", "--bogus"})
		if usageErr.Usage != "Usage: codex doctor --config <key=value>" {
			t.Fatalf("usage = %q", usageErr.Usage)
		}
	})
	t.Run("features list -c a=b --bogus echoes the override", func(t *testing.T) {
		usageErr := requireClapUsageError(t, []string{"features", "list", "-c", "a=b", "--bogus"})
		if usageErr.Usage != "Usage: codex features list --config <key=value>" {
			t.Fatalf("usage = %q", usageErr.Usage)
		}
	})
	t.Run("completion -c a=b --bogus keeps the positional slot", func(t *testing.T) {
		usageErr := requireClapUsageError(t, []string{"completion", "-c", "a=b", "--bogus"})
		if usageErr.Usage != "Usage: codex completion --config <key=value> [SHELL]" {
			t.Fatalf("usage = %q", usageErr.Usage)
		}
	})
	t.Run("leaves record the override", func(t *testing.T) {
		cases := []struct {
			args []string
			got  func(*Parsed) []string
		}{
			{[]string{"doctor", "-c", "a=b"}, func(p *Parsed) []string { return p.Doctor.ConfigOverrides }},
			{[]string{"update", "-c", "a=b"}, func(p *Parsed) []string { return p.Update.ConfigOverrides }},
			{[]string{"completion", "-c", "a=b"}, func(p *Parsed) []string { return p.Completion.ConfigOverrides }},
			{[]string{"features", "list", "-c", "a=b"}, func(p *Parsed) []string { return p.Features.ConfigOverrides }},
			{[]string{"debug", "models", "-c", "a=b"}, func(p *Parsed) []string { return p.Debug.ConfigOverrides }},
		}
		for _, testCase := range cases {
			parsed, err := Parse(testCase.args)
			if err != nil {
				t.Fatalf("Parse(%v) error = %v", testCase.args, err)
			}
			if got := strings.Join(testCase.got(parsed), ","); got != "a=b" {
				t.Fatalf("Parse(%v) overrides = %q, want %q", testCase.args, got, "a=b")
			}
		}
	})
}
