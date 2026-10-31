package cli

import "testing"

// TestReviewScopeDiagnosticsLikeRust pins clap's post-parse validation for
// `codex review` and `codex exec review`: ReviewArgs declares the three scope
// flags and the PROMPT positional mutually exclusive, --title requires
// --commit, and every single-value option rejects a second occurrence.
// clap orders conflicts by argv and echoes derived requirements into the
// smart usage block.
func TestReviewScopeDiagnosticsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "required-commit",
			args:    []string{"review", "--title=t"},
			message: "the following required arguments were not provided:\n  --commit <SHA>",
			usage:   "Usage: codex review --commit <SHA> --title <TITLE> [PROMPT]",
		},
		{
			name:    "exec-required-commit",
			args:    []string{"exec", "review", "--title", "t"},
			message: "the following required arguments were not provided:\n  --commit <SHA>",
			usage:   "Usage: codex exec review --commit <SHA> --title <TITLE> [PROMPT]",
		},
		{
			name:    "required-keeps-title-alone",
			args:    []string{"review", "--title", "t"},
			message: "the following required arguments were not provided:\n  --commit <SHA>",
			usage:   "Usage: codex review --commit <SHA> --title <TITLE> [PROMPT]",
		},
		{
			name:    "required-echoes-strict-config",
			args:    []string{"review", "--title=t", "--strict-config"},
			message: "the following required arguments were not provided:\n  --commit <SHA>",
			usage:   "Usage: codex review --commit <SHA> --title <TITLE> --strict-config [PROMPT]",
		},
		{
			name:    "required-echoes-config",
			args:    []string{"review", "--title=t", "-c", "k=v"},
			message: "the following required arguments were not provided:\n  --commit <SHA>",
			usage:   "Usage: codex review --commit <SHA> --title <TITLE> --config <key=value> [PROMPT]",
		},
		{
			name:    "required-echoes-model",
			args:    []string{"exec", "review", "--title=t", "--model", "gpt-5"},
			message: "the following required arguments were not provided:\n  --commit <SHA>",
			usage:   "Usage: codex exec review --commit <SHA> --title <TITLE> --model <MODEL> [PROMPT]",
		},
		{
			name:    "required-empty-title",
			args:    []string{"review", "--title="},
			message: "the following required arguments were not provided:\n  --commit <SHA>",
			usage:   "Usage: codex review --commit <SHA> --title <TITLE> [PROMPT]",
		},
		{
			name:    "uncommitted-base",
			args:    []string{"review", "--uncommitted", "--base", "main"},
			message: "the argument '--uncommitted' cannot be used with '--base <BRANCH>'",
			usage:   "Usage: codex review --uncommitted [PROMPT]",
		},
		{
			name:    "uncommitted-commit",
			args:    []string{"review", "--uncommitted", "--commit", "abc"},
			message: "the argument '--uncommitted' cannot be used with '--commit <SHA>'",
			usage:   "Usage: codex review --uncommitted [PROMPT]",
		},
		{
			name:    "base-commit",
			args:    []string{"review", "--base", "main", "--commit", "abc"},
			message: "the argument '--base <BRANCH>' cannot be used with '--commit <SHA>'",
			usage:   "Usage: codex review --base <BRANCH> [PROMPT]",
		},
		{
			name:    "uncommitted-prompt",
			args:    []string{"review", "--uncommitted", "prompt"},
			message: "the argument '--uncommitted' cannot be used with '[PROMPT]'",
			usage:   "Usage: codex review --uncommitted [PROMPT]",
		},
		{
			name:    "base-prompt",
			args:    []string{"review", "--base", "main", "prompt"},
			message: "the argument '--base <BRANCH>' cannot be used with '[PROMPT]'",
			usage:   "Usage: codex review --base <BRANCH> [PROMPT]",
		},
		{
			name:    "commit-prompt",
			args:    []string{"review", "--commit", "abc", "prompt"},
			message: "the argument '--commit <SHA>' cannot be used with '[PROMPT]'",
			usage:   "Usage: codex review --commit <SHA> [PROMPT]",
		},
		{
			name:    "uncommitted-base-prompt",
			args:    []string{"review", "--uncommitted", "--base", "main", "prompt"},
			message: "the argument '--uncommitted' cannot be used with:\n  --base <BRANCH>\n  [PROMPT]",
			usage:   "Usage: codex review --uncommitted [PROMPT]",
		},
		{
			name:    "prompt-before-base",
			args:    []string{"review", "--uncommitted", "p", "--base", "x"},
			message: "the argument '--uncommitted' cannot be used with:\n  [PROMPT]\n  --base <BRANCH>",
			usage:   "Usage: codex review --uncommitted [PROMPT]",
		},
		{
			name:    "exec-uncommitted-base",
			args:    []string{"exec", "review", "--uncommitted", "--base", "main"},
			message: "the argument '--uncommitted' cannot be used with '--base <BRANCH>'",
			usage:   "Usage: codex exec review --uncommitted [PROMPT]",
		},
		{
			name:    "exec-uncommitted-prompt",
			args:    []string{"exec", "review", "--uncommitted", "prompt"},
			message: "the argument '--uncommitted' cannot be used with '[PROMPT]'",
			usage:   "Usage: codex exec review --uncommitted [PROMPT]",
		},
		{
			name:    "exec-uncommitted-title-prompt",
			args:    []string{"exec", "review", "--uncommitted", "--title=t", "p"},
			message: "the argument '--uncommitted' cannot be used with '[PROMPT]'",
			usage:   "Usage: codex exec review --commit <SHA> --uncommitted --title <TITLE> [PROMPT]",
		},
		{
			name:    "title-uncommitted-base",
			args:    []string{"review", "--title=t", "--uncommitted", "--base", "main"},
			message: "the argument '--uncommitted' cannot be used with '--base <BRANCH>'",
			usage:   "Usage: codex review --commit <SHA> --title <TITLE> --uncommitted [PROMPT]",
		},
		{
			name:    "commit-before-base",
			args:    []string{"review", "--title", "t", "--commit", "abc", "--base", "main"},
			message: "the argument '--commit <SHA>' cannot be used with '--base <BRANCH>'",
			usage:   "Usage: codex review --title <TITLE> --commit <SHA> [PROMPT]",
		},
		{
			name:    "base-before-commit",
			args:    []string{"review", "--title=t", "--base", "main", "--commit", "abc"},
			message: "the argument '--base <BRANCH>' cannot be used with '--commit <SHA>'",
			usage:   "Usage: codex review --title <TITLE> --base <BRANCH> [PROMPT]",
		},
		{
			name:    "title-multiple-times",
			args:    []string{"review", "--title", "a", "--title", "b"},
			message: "the argument '--title <TITLE>' cannot be used multiple times",
			usage:   "Usage: codex review [OPTIONS] [PROMPT]",
		},
		{
			name:    "uncommitted-multiple-times",
			args:    []string{"review", "--uncommitted", "--uncommitted"},
			message: "the argument '--uncommitted' cannot be used multiple times",
			usage:   "Usage: codex review [OPTIONS] [PROMPT]",
		},
		{
			name:    "strict-config-multiple-times",
			args:    []string{"review", "--strict-config", "--strict-config"},
			message: "the argument '--strict-config' cannot be used multiple times",
			usage:   "Usage: codex review [OPTIONS] [PROMPT]",
		},
		{
			name:    "exec-json-multiple-times",
			args:    []string{"exec", "review", "--json", "--json", "--title", "t"},
			message: "the argument '--json' cannot be used multiple times",
			usage:   "Usage: codex exec review [OPTIONS] [PROMPT]",
		},
		{
			name:    "title-repeat-before-unknown",
			args:    []string{"review", "--title", "a", "--title", "b", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex review [OPTIONS] [PROMPT]",
		},
		{
			name:    "unknown-before-title-repeat",
			args:    []string{"review", "--bogus", "--title", "a", "--title", "b"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex review [OPTIONS] [PROMPT]",
		},
		{
			name:    "base-multiple-times",
			args:    []string{"review", "--base", "x", "--base", "y"},
			message: "the argument '--base <BRANCH>' cannot be used multiple times",
			usage:   "Usage: codex review [OPTIONS] [PROMPT]",
		},
		{
			name:    "title-repeat-beats-conflict",
			args:    []string{"review", "--title", "a", "--title", "b", "--uncommitted", "--base", "x"},
			message: "the argument '--title <TITLE>' cannot be used multiple times",
			usage:   "Usage: codex review [OPTIONS] [PROMPT]",
		},
		{
			name:    "uncommitted-repeat-before-unknown",
			args:    []string{"review", "--uncommitted", "--uncommitted", "--bogus"},
			message: "the argument '--uncommitted' cannot be used multiple times",
			usage:   "Usage: codex review [OPTIONS] [PROMPT]",
		},
		{
			name:    "strict-config-repeat-before-unknown",
			args:    []string{"review", "--strict-config", "--strict-config", "--bogus"},
			message: "the argument '--strict-config' cannot be used multiple times",
			usage:   "Usage: codex review [OPTIONS] [PROMPT]",
		},
		{
			name:    "exec-json-repeat-before-unknown",
			args:    []string{"exec", "review", "--json", "--json", "--bogus"},
			message: "the argument '--json' cannot be used multiple times",
			usage:   "Usage: codex exec review [OPTIONS] [PROMPT]",
		},
		{
			name:    "base-repeat-before-title",
			args:    []string{"review", "--base", "x", "--base", "y", "--title", "t"},
			message: "the argument '--base <BRANCH>' cannot be used multiple times",
			usage:   "Usage: codex review [OPTIONS] [PROMPT]",
		},
		{
			name:    "exec-model-repeat-before-unknown",
			args:    []string{"exec", "review", "--model", "a", "--model", "b", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex exec review [OPTIONS] [PROMPT]",
		},
		{
			name:    "title-then-unknown",
			args:    []string{"review", "--title", "a", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex review --title <TITLE> [PROMPT]",
		},
		{
			name:    "title-then-misspelled-flag",
			args:    []string{"review", "--title=a", "--stripe"},
			message: "unexpected argument '--stripe' found\n\n  tip: a similar argument exists: '--title'",
			usage:   "Usage: codex review --title <TITLE> [PROMPT]",
		},
		{
			name:    "base-repeat-before-unknown",
			args:    []string{"review", "--base", "x", "--base", "y", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex review [OPTIONS] [PROMPT]",
		},
		{
			name:    "exec-title-repeat-before-unknown",
			args:    []string{"exec", "review", "--title", "a", "--title", "b", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex exec review [OPTIONS] [PROMPT]",
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
