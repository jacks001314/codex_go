package cli

import (
	"errors"
	"strings"
	"testing"
)

// TestExecHelpPagesLikeRust freezes the exec help surface captured from
// codex-cli 0.166: page selection, the usage block of each scope and the exact
// line counts. Any drift in the generated tables or in the parser wiring shows
// up here instead of in a manual `exec --help` diff.
func TestExecHelpPagesLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		kind   ExecHelpKind
		short  bool
		lines  int
		usage  string
		first  string
		absent string
	}{
		{name: "exec long", kind: ExecHelpExec, lines: 118, usage: "Usage: codex exec [OPTIONS] [PROMPT]", first: "Run Codex non-interactively", absent: "Usage: codex exec resume"},
		{name: "exec short", kind: ExecHelpExec, short: true, lines: 83, usage: "Usage: codex exec [OPTIONS] [PROMPT]", first: "Run Codex non-interactively"},
		{name: "resume long", kind: ExecHelpResume, lines: 86, usage: "Usage: codex exec resume [OPTIONS] [SESSION_ID] [PROMPT]", absent: "Usage: codex exec fork"},
		{name: "resume short", kind: ExecHelpResume, short: true, lines: 59, usage: "Usage: codex exec resume [OPTIONS] [SESSION_ID] [PROMPT]"},
		{name: "fork long", kind: ExecHelpFork, lines: 79, usage: "Usage: codex exec fork [OPTIONS] <SESSION_ID> [PROMPT]"},
		{name: "fork short", kind: ExecHelpFork, short: true, lines: 54, usage: "Usage: codex exec fork [OPTIONS] <SESSION_ID> [PROMPT]"},
		{name: "review long", kind: ExecHelpReview, lines: 85, usage: "Usage: codex exec review [OPTIONS] [PROMPT]"},
		{name: "review short", kind: ExecHelpReview, short: true, lines: 59, usage: "Usage: codex exec review [OPTIONS] [PROMPT]"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			text := ExecHelpText(testCase.kind, testCase.short)
			if got := strings.Count(text, "\n"); got != testCase.lines {
				t.Fatalf("page lines = %d, want %d", got, testCase.lines)
			}
			if !strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\n\n") {
				t.Fatalf("page must end with exactly one newline: %q", text[len(text)-4:])
			}
			if !strings.Contains(text, testCase.usage) {
				t.Fatalf("page missing %q", testCase.usage)
			}
			if testCase.first != "" && !strings.HasPrefix(text, testCase.first) {
				t.Fatalf("page starts with %q", text[:40])
			}
			if testCase.absent != "" && strings.Contains(text, testCase.absent) {
				t.Fatalf("page unexpectedly contains %q", testCase.absent)
			}
			if strings.Contains(text, "\t") {
				t.Fatal("clap help pages must not contain tabs")
			}
		})
	}
}

// TestExecHelpListsAutoReviewFlagLikeRust keeps the captured 0.166 option list
// honest: `--approve-for-me` is part of the exec help surface.
func TestExecHelpListsAutoReviewFlagLikeRust(t *testing.T) {
	text := ExecHelpText(ExecHelpExec, false)
	for _, want := range []string{
		"      --approve-for-me",
		"          Route approval requests through automatic review using the workspace-write sandbox",
		"  -s, --sandbox <SANDBOX_MODE>",
		"  resume  Resume a previous session by id or pick the most recent with --last",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("exec help missing %q", want)
		}
	}
}

func TestParseExecHelpAndVersionLikeRust(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		kind    ExecHelpKind
		short   bool
		version bool
	}{
		{name: "exec long", args: []string{"exec", "--help"}, kind: ExecHelpExec},
		{name: "exec short", args: []string{"exec", "-h"}, kind: ExecHelpExec, short: true},
		{name: "exec subcommand help", args: []string{"exec", "help"}, kind: ExecHelpExec},
		{name: "exec help resume", args: []string{"exec", "help", "resume"}, kind: ExecHelpResume},
		{name: "exec help fork", args: []string{"exec", "help", "fork"}, kind: ExecHelpFork},
		{name: "exec help review", args: []string{"exec", "help", "review"}, kind: ExecHelpReview},
		{name: "resume long", args: []string{"exec", "resume", "--help"}, kind: ExecHelpResume},
		{name: "resume short", args: []string{"exec", "resume", "-h"}, kind: ExecHelpResume, short: true},
		{name: "fork long", args: []string{"exec", "fork", "--help"}, kind: ExecHelpFork},
		{name: "fork short", args: []string{"exec", "fork", "-h"}, kind: ExecHelpFork, short: true},
		{name: "review long", args: []string{"exec", "review", "--help"}, kind: ExecHelpReview},
		{name: "review short", args: []string{"exec", "review", "-h"}, kind: ExecHelpReview, short: true},
		{name: "exec version", args: []string{"exec", "-V"}, version: true},
		{name: "exec long version", args: []string{"exec", "--version"}, version: true},
		{name: "resume version", args: []string{"exec", "resume", "--version"}, version: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := Parse(testCase.args)
			if err != nil {
				t.Fatalf("Parse(%v) error = %v", testCase.args, err)
			}
			if parsed.Command != CommandExec {
				t.Fatalf("command = %q", parsed.Command)
			}
			if parsed.Exec.Help == nil {
				t.Fatalf("Parse(%v) did not record a help request", testCase.args)
			}
			if parsed.Exec.Help.Version != testCase.version {
				t.Fatalf("version = %v, want %v", parsed.Exec.Help.Version, testCase.version)
			}
			if parsed.Exec.Help.Kind != testCase.kind {
				t.Fatalf("kind = %q, want %q", parsed.Exec.Help.Kind, testCase.kind)
			}
			if parsed.Exec.Help.Short != testCase.short {
				t.Fatalf("short = %v, want %v", parsed.Exec.Help.Short, testCase.short)
			}
		})
	}
}

func TestParseExecClapDiagnosticsLikeRust(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "unknown flag",
			args:    []string{"exec", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   execUsageText,
		},
		{
			name:    "unknown resume flag",
			args:    []string{"exec", "resume", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   execResumeUsageText,
		},
		{
			name:    "unknown resume flag after --last",
			args:    []string{"exec", "resume", "--last", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex exec resume --last [SESSION_ID] [PROMPT]",
		},
		{
			name:    "unknown fork flag",
			args:    []string{"exec", "fork", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   execForkUsageText,
		},
		{
			name:    "unknown review flag",
			args:    []string{"exec", "review", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   execReviewUsageText,
		},
		{
			name:    "unknown help topic",
			args:    []string{"exec", "help", "bogus"},
			message: "unrecognized subcommand 'bogus'",
			usage:   execUsageText,
		},
		{
			name:    "invalid sandbox",
			args:    []string{"exec", "--sandbox", "bogus"},
			message: "invalid value 'bogus' for '--sandbox <SANDBOX_MODE>'\n  [possible values: read-only, workspace-write, danger-full-access]",
		},
		{
			name:    "similar sandbox value",
			args:    []string{"exec", "--sandbox", "workspace"},
			message: "invalid value 'workspace' for '--sandbox <SANDBOX_MODE>'\n  [possible values: read-only, workspace-write, danger-full-access]\n\n  tip: a similar value exists: 'workspace-write'",
		},
		{
			name:    "sandbox value is case sensitive",
			args:    []string{"exec", "--sandbox", "READ-ONLY"},
			message: "invalid value 'READ-ONLY' for '--sandbox <SANDBOX_MODE>'\n  [possible values: read-only, workspace-write, danger-full-access]",
		},
		{
			name:    "missing sandbox value",
			args:    []string{"exec", "--sandbox", ""},
			message: "a value is required for '--sandbox <SANDBOX_MODE>' but none was supplied\n  [possible values: read-only, workspace-write, danger-full-access]",
		},
		{
			name:    "invalid color",
			args:    []string{"exec", "--color", "bogus"},
			message: "invalid value 'bogus' for '--color <COLOR>'\n  [possible values: always, never, auto]",
		},
		{
			name:    "color value is case sensitive",
			args:    []string{"exec", "--color", "ALWAYS"},
			message: "invalid value 'ALWAYS' for '--color <COLOR>'\n  [possible values: always, never, auto]",
		},
		{
			name:    "missing color value",
			args:    []string{"exec", "--color="},
			message: "a value is required for '--color <COLOR>' but none was supplied\n  [possible values: always, never, auto]",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := Parse(testCase.args)
			if err == nil {
				t.Fatalf("Parse(%v) = %#v, want a diagnostic", testCase.args, parsed)
			}
			var usage *UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("Parse(%v) error = %T %v, want *UsageError", testCase.args, err, err)
			}
			if usage.Message != testCase.message {
				t.Fatalf("message = %q, want %q", usage.Message, testCase.message)
			}
			if usage.Usage != testCase.usage {
				t.Fatalf("usage = %q, want %q", usage.Usage, testCase.usage)
			}
		})
	}
}

func TestParseExecApproveForMeLikeRust(t *testing.T) {
	parsed, err := Parse([]string{"exec", "--approve-for-me", "hello"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := []string{
		`approvals_reviewer="auto_review"`,
		`approval_policy="on-request"`,
		`sandbox_mode="workspace-write"`,
	}
	if got := parsed.Exec.ConfigOverrides; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("overrides = %v, want %v", got, want)
	}
	if parsed.Exec.Prompt != "hello" {
		t.Fatalf("prompt = %q", parsed.Exec.Prompt)
	}

	// The alias and a user override keep Rust's ordering: user values first,
	// the auto-review trio appended last so the flag wins.
	parsed, err = Parse([]string{"exec", "-c", "model=gpt-5", "--not-so-yolo", "hello"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want = append([]string{"model=gpt-5"}, want...)
	if got := parsed.Exec.ConfigOverrides; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("overrides = %v, want %v", got, want)
	}

	// `-c approvals_reviewer=...` must not survive after the flag.
	if !strings.HasSuffix(strings.Join(parsed.Exec.ConfigOverrides, "|"), strings.Join([]string{`approvals_reviewer="auto_review"`, `approval_policy="on-request"`, `sandbox_mode="workspace-write"`}, "|")) {
		t.Fatalf("auto-review overrides are not last: %v", parsed.Exec.ConfigOverrides)
	}
}

func TestParseExecApproveForMeConflictsLikeRust(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		message string
	}{
		{
			name:    "sandbox",
			args:    []string{"exec", "--approve-for-me", "-s", "read-only"},
			message: "the argument '--approve-for-me' cannot be used with '--sandbox <SANDBOX_MODE>'",
		},
		{
			name:    "bypass",
			args:    []string{"exec", "--approve-for-me", "--dangerously-bypass-approvals-and-sandbox"},
			message: "the argument '--approve-for-me' cannot be used with '--dangerously-bypass-approvals-and-sandbox'",
		},
		{
			name:    "bypass alias",
			args:    []string{"exec", "--not-so-yolo", "--yolo"},
			message: "the argument '--approve-for-me' cannot be used with '--dangerously-bypass-approvals-and-sandbox'",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Parse(testCase.args)
			var usage *UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("Parse(%v) error = %T %v, want *UsageError", testCase.args, err, err)
			}
			if usage.Message != testCase.message {
				t.Fatalf("message = %q, want %q", usage.Message, testCase.message)
			}
			if usage.Usage != execUsageText {
				t.Fatalf("usage = %q", usage.Usage)
			}
		})
	}
}
