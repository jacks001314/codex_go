package cli

import "testing"

// TestLongFlagDidYouMeanLikeRust pins clap's did-you-mean pass over a command's
// declared long flags. clap scores the unknown spelling with strsim's Jaro
// metric, keeps candidates above 0.7, names the highest scoring one in the tip
// and folds it into the smart usage block.
func TestLongFlagDidYouMeanLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "exec root keeps the static page",
			args:    []string{"exec", "--jsons", "hi"},
			message: "unexpected argument '--jsons' found\n\n  tip: a similar argument exists: '--json'",
			usage:   execUsageText,
		},
		{
			name:    "exec resume last",
			args:    []string{"exec", "resume", "--las"},
			message: "unexpected argument '--las' found\n\n  tip: a similar argument exists: '--last'",
			usage:   "Usage: codex exec resume --last [SESSION_ID] [PROMPT]",
		},
		{
			name:    "exec fork single-value image",
			args:    []string{"exec", "fork", "--imag", "a.png"},
			message: "unexpected argument '--imag' found\n\n  tip: a similar argument exists: '--image'",
			usage:   "Usage: codex exec fork --image <FILE> <SESSION_ID> [PROMPT]",
		},
		{
			name:    "exec review value flag",
			args:    []string{"exec", "review", "--bse", "main"},
			message: "unexpected argument '--bse' found\n\n  tip: a similar argument exists: '--base'",
			usage:   "Usage: codex exec review --base <BRANCH> [PROMPT]",
		},
		{
			name:    "hidden alias keeps its spelling in the tip",
			args:    []string{"exec", "resume", "--experimental-jso"},
			message: "unexpected argument '--experimental-jso' found\n\n  tip: a similar argument exists: '--experimental-json'",
			usage:   "Usage: codex exec resume --json [SESSION_ID] [PROMPT]",
		},
		{
			name:    "root command",
			args:    []string{"--stric-config", "login"},
			message: "unexpected argument '--stric-config' found\n\n  tip: a similar argument exists: '--strict-config'",
			usage:   rootUsageText,
		},
		{
			name:    "session variadic image",
			args:    []string{"resume", "--imge", "a.png"},
			message: "unexpected argument '--imge' found\n\n  tip: a similar argument exists: '--image'",
			usage:   "Usage: codex resume --image <FILE>... [SESSION_ID] [PROMPT]",
		},
		{
			name:    "session chunk keeps the required positional",
			args:    []string{"archive", "--stroct-config", "abc"},
			message: "unexpected argument '--stroct-config' found\n\n  tip: a similar argument exists: '--strict-config'",
			usage:   "Usage: codex archive --strict-config <SESSION>",
		},
		{
			name:    "delete force",
			args:    []string{"delete", "--forc", "nosuch"},
			message: "unexpected argument '--forc' found\n\n  tip: a similar argument exists: '--force'",
			usage:   "Usage: codex delete --force <SESSION>",
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

// TestLongFlagSmartUsageEchoesConsumedOptionsLikeRust pins clap's smart usage
// block: every option accepted before the unknown flag is echoed in argv order,
// the [OPTIONS] placeholder disappears and bound positionals use angle brackets.
func TestLongFlagSmartUsageEchoesConsumedOptionsLikeRust(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "exec resume value option",
			args: []string{"exec", "resume", "-m", "x", "--bogus"},
			want: "Usage: codex exec resume --model <MODEL> [SESSION_ID] [PROMPT]",
		},
		{
			name: "exec fork value option",
			args: []string{"exec", "fork", "-m", "x", "--bogus"},
			want: "Usage: codex exec fork --model <MODEL> <SESSION_ID> [PROMPT]",
		},
		{
			name: "exec review two options",
			args: []string{"exec", "review", "--uncommitted", "-m", "x", "--bogus"},
			want: "Usage: codex exec review --uncommitted --model <MODEL> [PROMPT]",
		},
		{
			name: "suggested flag follows the consumed one",
			args: []string{"exec", "resume", "-m", "x", "--las"},
			want: "Usage: codex exec resume --model <MODEL> --last [SESSION_ID] [PROMPT]",
		},
		{
			name: "exec resume bound positional",
			args: []string{"exec", "resume", "abc", "--bogus"},
			want: "Usage: codex exec resume <SESSION_ID> [PROMPT]",
		},
		{
			name: "exec fork single-value image",
			args: []string{"exec", "fork", "--image", "a.png", "--bogus"},
			want: "Usage: codex exec fork --image <FILE> <SESSION_ID> [PROMPT]",
		},
		{
			name: "nothing consumed keeps the static page",
			args: []string{"exec", "resume", "--bogus"},
			want: execResumeUsageText,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			usageErr := requireClapUsageError(t, testCase.args)
			if usageErr.Usage != testCase.want {
				t.Fatalf("usage = %q, want %q", usageErr.Usage, testCase.want)
			}
		})
	}
}

// TestRootUnknownFlagSubcommandFallbackLikeRust pins clap's second did-you-mean
// pass: when the root declares no similar flag, clap scores the spelling against
// the flags of the subcommand named later in argv.
func TestRootUnknownFlagSubcommandFallbackLikeRust(t *testing.T) {
	usageErr := requireClapUsageError(t, []string{"--skp-git-repo-check", "exec"})
	if usageErr.Message != "unexpected argument '--skp-git-repo-check' found\n\n  tip: 'exec --skip-git-repo-check' exists" {
		t.Fatalf("message = %q", usageErr.Message)
	}
	if usageErr.Usage != rootUsageText {
		t.Fatalf("usage = %q", usageErr.Usage)
	}
	usageErr = requireClapUsageError(t, []string{"--json", "exec"})
	if usageErr.Message != "unexpected argument '--json' found\n\n  tip: 'exec --json' exists" {
		t.Fatalf("message = %q", usageErr.Message)
	}
	usageErr = requireClapUsageError(t, []string{"--jsn", "exec"})
	if usageErr.Message != "unexpected argument '--jsn' found\n\n  tip: 'exec --json' exists" {
		t.Fatalf("message = %q", usageErr.Message)
	}
}

// TestUnknownLongFlagWithoutSuggestionLikeRust pins the fallback diagnostic when
// no declared flag scores above clap's cutoff: the `--` workaround tip only
// appears on commands that declare positional arguments.
func TestUnknownLongFlagWithoutSuggestionLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
	}{
		{
			name:    "exec root",
			args:    []string{"exec", "--zzz", "hi"},
			message: "unexpected argument '--zzz' found\n\n  tip: to pass '--zzz' as a value, use '-- --zzz'",
		},
		{
			name:    "exec review",
			args:    []string{"exec", "review", "--zzz"},
			message: "unexpected argument '--zzz' found\n\n  tip: to pass '--zzz' as a value, use '-- --zzz'",
		},
		{
			name:    "exec short flag",
			args:    []string{"exec", "-z"},
			message: "unexpected argument '-z' found\n\n  tip: to pass '-z' as a value, use '-- -z'",
		},
		{
			name:    "option-only command has no workaround tip",
			args:    []string{"mcp", "list", "--naem"},
			message: "unexpected argument '--naem' found",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			usageErr := requireClapUsageError(t, testCase.args)
			if usageErr.Message != testCase.message {
				t.Fatalf("message = %q, want %q", usageErr.Message, testCase.message)
			}
		})
	}
}
