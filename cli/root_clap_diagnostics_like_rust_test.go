package cli

import (
	"errors"
	"strings"
	"testing"
)

func rootUsageString() string {
	return "Usage: codex [OPTIONS] [PROMPT]\n       codex [OPTIONS] <COMMAND> [ARGS]"
}

// TestRootClapDiagnosticsLikeRust pins clap's root diagnostics for unknown
// options, missing option values and a second positional, all of which Rust
// reports with exit status 2.
func TestRootClapDiagnosticsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
	}{
		{
			name:    "unknown long option",
			args:    []string{"--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
		},
		{
			name:    "unknown long option with value",
			args:    []string{"--bogus=1"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
		},
		{
			name:    "unknown short option",
			args:    []string{"-Z"},
			message: "unexpected argument '-Z' found\n\n  tip: to pass '-Z' as a value, use '-- -Z'",
		},
		{
			name:    "subcommand-only flag",
			args:    []string{"--json", "exec"},
			message: "unexpected argument '--json' found\n\n  tip: 'exec --json' exists",
		},
		{
			name:    "subcommand flag without the subcommand",
			args:    []string{"--json"},
			message: "unexpected argument '--json' found\n\n  tip: to pass '--json' as a value, use '-- --json'",
		},
		{
			name:    "missing model value",
			args:    []string{"--model"},
			message: "a value is required for '--model <MODEL>' but none was supplied",
		},
		{
			name:    "missing short model value",
			args:    []string{"-m"},
			message: "a value is required for '--model <MODEL>' but none was supplied",
		},
		{
			name:    "missing approval value lists possible values",
			args:    []string{"--ask-for-approval"},
			message: "a value is required for '--ask-for-approval <APPROVAL_POLICY>' but none was supplied\n  [possible values: on-request, never]",
		},
		{
			name:    "missing config value",
			args:    []string{"-c"},
			message: "a value is required for '--config <key=value>' but none was supplied",
		},
		{
			name:    "second positional",
			args:    []string{"hello", "world"},
			message: "unexpected argument 'world' found",
		},
		{
			name:    "flag after the prompt",
			args:    []string{"hello", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Parse(testCase.args)
			var usage *UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("Parse(%v) error = %v, want UsageError", testCase.args, err)
			}
			if usage.Message != testCase.message {
				t.Fatalf("message = %q, want %q", usage.Message, testCase.message)
			}
			if testCase.name == "missing model value" || testCase.name == "missing short model value" ||
				testCase.name == "missing approval value lists possible values" || testCase.name == "missing config value" {
				if usage.Usage != "" {
					t.Fatalf("usage = %q, want no usage block for a missing value", usage.Usage)
				}
				return
			}
			if usage.Usage != rootUsageString() {
				t.Fatalf("usage = %q, want root usage", usage.Usage)
			}
		})
	}
}

// TestDoctorClapDiagnosticsLikeRust pins `codex doctor`'s diagnostics: its
// clap command declares no positionals, so unexpected arguments carry no `--`
// tip, and the usage block names the subcommand.
func TestDoctorClapDiagnosticsLikeRust(t *testing.T) {
	const usage = "Usage: codex doctor [OPTIONS]"
	for _, args := range [][]string{
		{"doctor", "--bogus"},
		{"doctor", "--bogus=1"},
		{"doctor", "foo"},
	} {
		_, err := Parse(args)
		var usageErr *UsageError
		if !errors.As(err, &usageErr) {
			t.Fatalf("Parse(%v) error = %v, want UsageError", args, err)
		}
		wantMessage := "unexpected argument '" + flagNameOnly(args[1]) + "' found"
		if usageErr.Message != wantMessage {
			t.Fatalf("Parse(%v) message = %q, want %q", args, usageErr.Message, wantMessage)
		}
		if usageErr.Usage != usage {
			t.Fatalf("Parse(%v) usage = %q, want %q", args, usageErr.Usage, usage)
		}
	}
	if _, err := Parse([]string{"doctor", "--probe-filesystem-path"}); err == nil || !strings.Contains(err.Error(), "a value is required for '--probe-filesystem-path <PROBE_FILESYSTEM_PATH>'") {
		t.Fatalf("Parse(doctor --probe-filesystem-path) error = %v", err)
	}
}

// TestDoubleDashKeepsFollowingWordsAsPromptLikeRust pins that `--` ends option
// parsing and the following word is the PROMPT even when it names a subcommand.
func TestDoubleDashKeepsFollowingWordsAsPromptLikeRust(t *testing.T) {
	parsed, err := Parse([]string{"--", "exec"})
	if err != nil {
		t.Fatalf("Parse(-- exec) error = %v", err)
	}
	if parsed.Command != CommandInteractive || parsed.Root.Prompt != "exec" {
		t.Fatalf("command = %q prompt = %q, want interactive prompt %q", parsed.Command, parsed.Root.Prompt, "exec")
	}
	if _, err := Parse([]string{"--", "-x", "y"}); err == nil {
		t.Fatal("Parse(-- -x y) error = nil, want a second-positional diagnostic")
	}
}