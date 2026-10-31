package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestSessionVariadicImagesLikeRust pins Rust's `--image` contract on the
// session subcommands. ResumeCommand/ForkCommand/SessionArchiveCommand all
// flatten SharedCliOptions, whose `--image` declares num_args = 1.., so every
// following non-flag token is an image; an inline value or `--` ends the run.
func TestSessionVariadicImagesLikeRust(t *testing.T) {
	forceID := "123e4567-e89b-12d3-a456-426614174000"
	cases := []struct {
		name   string
		args   []string
		images []string
		target string
		prompt string
		last   bool
		force  bool
	}{
		{
			name:   "resume swallows two images",
			args:   []string{"resume", "-i", "a.png", "b.png"},
			images: []string{"a.png", "b.png"},
		},
		{
			name:   "resume swallows every following positional",
			args:   []string{"resume", "-i", "a.png", "b.png", "c.png"},
			images: []string{"a.png", "b.png", "c.png"},
		},
		{
			name:   "resume splits comma separated images",
			args:   []string{"resume", "-i", "a.png,b.png"},
			images: []string{"a.png", "b.png"},
		},
		{
			name:   "resume last keeps no positional",
			args:   []string{"resume", "--last", "-i", "a.png", "b.png"},
			images: []string{"a.png", "b.png"},
			last:   true,
		},
		{
			name:   "resume binds session id before images",
			args:   []string{"resume", "nosuch", "-i", "a.png", "b.png"},
			images: []string{"a.png", "b.png"},
			target: "nosuch",
		},
		{
			name:   "resume stops images at flag",
			args:   []string{"resume", "-i", "a.png", "--last", "explain"},
			images: []string{"a.png"},
			prompt: "explain",
			last:   true,
		},
		{
			name:   "fork swallows two images",
			args:   []string{"fork", "-i", "a.png", "b.png"},
			images: []string{"a.png", "b.png"},
		},
		{
			name:   "archive swallows two images",
			args:   []string{"archive", "nosuch", "-i", "a.png", "b.png"},
			images: []string{"a.png", "b.png"},
			target: "nosuch",
		},
		{
			name:   "archive resumes positionals after terminator",
			args:   []string{"archive", "-i", "a.png", "b.png", "--", "c"},
			images: []string{"a.png", "b.png"},
			target: "c",
		},
		{
			name:   "inline image value stops the variadic run",
			args:   []string{"archive", "--image=a.png", "b.png"},
			images: []string{"a.png"},
			target: "b.png",
		},
		{
			name:   "delete force with images",
			args:   []string{"delete", "--force", forceID, "-i", "a.png", "b.png"},
			images: []string{"a.png", "b.png"},
			target: forceID,
			force:  true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := Parse(testCase.args)
			if err != nil {
				t.Fatalf("Parse(%v) error = %v", testCase.args, err)
			}
			session := parsed.Session
			if !reflect.DeepEqual(session.Shared.Images, testCase.images) {
				t.Fatalf("images = %v, want %v", session.Shared.Images, testCase.images)
			}
			if session.Target != testCase.target || session.Prompt != testCase.prompt {
				t.Fatalf("target/prompt = %q/%q, want %q/%q", session.Target, session.Prompt, testCase.target, testCase.prompt)
			}
			if session.Last != testCase.last || session.Force != testCase.force {
				t.Fatalf("last/force = %v/%v, want %v/%v", session.Last, session.Force, testCase.last, testCase.force)
			}
		})
	}
}

// TestSessionVariadicImagesConsumeSessionLikeRust pins the greedy run: with no
// positional in front of `-i`, every following word is an image, so the
// SESSION slot stays empty.
func TestSessionVariadicImagesConsumeSessionLikeRust(t *testing.T) {
	_, err := Parse([]string{"archive", "-i", "a.png", "nosuch"})
	var usageErr *UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("Parse error = %v, want a *UsageError", err)
	}
	if usageErr.Message != "the following required arguments were not provided:\n  <SESSION>" {
		t.Fatalf("message = %q", usageErr.Message)
	}
	if usageErr.Usage != "Usage: codex archive --image <FILE>... <SESSION>" {
		t.Fatalf("usage = %q", usageErr.Usage)
	}
}

// TestSessionUnexpectedArgumentDiagnosticsLikeRust pins clap's smart usage for
// the session subcommands: consumed options are echoed, and positional slots
// switch to angle brackets once bound.
func TestSessionUnexpectedArgumentDiagnosticsLikeRust(t *testing.T) {
	const passTip = " as a value, use '-- "
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "resume unknown flag keeps table usage",
			args:    []string{"resume", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus'" + passTip + "--bogus'",
			usage:   "Usage: codex resume [OPTIONS] [SESSION_ID] [PROMPT]",
		},
		{
			name:    "resume bound session id",
			args:    []string{"resume", "a", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus'" + passTip + "--bogus'",
			usage:   "Usage: codex resume <SESSION_ID> [PROMPT]",
		},
		{
			name:    "resume echoes last",
			args:    []string{"resume", "--last", "a", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus'" + passTip + "--bogus'",
			usage:   "Usage: codex resume --last <SESSION_ID> [PROMPT]",
		},
		{
			name:    "resume echoes variadic images",
			args:    []string{"resume", "-i", "a.png", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus'" + passTip + "--bogus'",
			usage:   "Usage: codex resume --image <FILE>... [SESSION_ID] [PROMPT]",
		},
		{
			name:    "resume echoes all with both positionals",
			args:    []string{"resume", "--all", "a", "b", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus'" + passTip + "--bogus'",
			usage:   "Usage: codex resume --all <SESSION_ID> <PROMPT>",
		},
		{
			name:    "resume rejects third positional",
			args:    []string{"resume", "a", "b", "c"},
			message: "unexpected argument 'c' found",
			usage:   "Usage: codex resume [OPTIONS] [SESSION_ID] [PROMPT]",
		},
		{
			name:    "fork rejects third positional",
			args:    []string{"fork", "a", "b", "c"},
			message: "unexpected argument 'c' found",
			usage:   "Usage: codex fork [OPTIONS] [SESSION_ID] [PROMPT]",
		},
		{
			name:    "archive rejects force",
			args:    []string{"archive", "--force"},
			message: "unexpected argument '--force' found\n\n  tip: to pass '--force'" + passTip + "--force'",
			usage:   "Usage: codex archive [OPTIONS] <SESSION>",
		},
		{
			name:    "archive drops options once session bound",
			args:    []string{"archive", "nosuch", "--force"},
			message: "unexpected argument '--force' found\n\n  tip: to pass '--force'" + passTip + "--force'",
			usage:   "Usage: codex archive <SESSION>",
		},
		{
			name:    "archive echoes images before session",
			args:    []string{"archive", "-i", "a.png", "nosuch", "--force"},
			message: "unexpected argument '--force' found\n\n  tip: to pass '--force'" + passTip + "--force'",
			usage:   "Usage: codex archive --image <FILE>... <SESSION>",
		},
		{
			name:    "unarchive rejects second positional",
			args:    []string{"unarchive", "nosuch", "extra"},
			message: "unexpected argument 'extra' found",
			usage:   "Usage: codex unarchive [OPTIONS] <SESSION>",
		},
		{
			name:    "delete rejects second positional",
			args:    []string{"delete", "nosuch", "extra"},
			message: "unexpected argument 'extra' found",
			usage:   "Usage: codex delete [OPTIONS] <SESSION>",
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

// TestSessionLastPromptConflictLikeRust pins clap's conflicts_with diagnostic:
// Rust marks the TUI prompt argument as conflicts_with = "last" for
// `codex resume` and `codex fork`.
func TestSessionLastPromptConflictLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"resume", "--last", "session-1", "continue here"},
		{"resume", "session-1", "--last", "continue here"},
		{"fork", "--last", "session-1", "continue here"},
		{"fork", "session-1", "--last", "continue here"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			usageErr := requireClapUsageError(t, args)
			wantCommand := "resume"
			if args[0] == "fork" {
				wantCommand = "fork"
			}
			if usageErr.Message != "the argument '--last' cannot be used with '[PROMPT]'" {
				t.Fatalf("message = %q", usageErr.Message)
			}
			wantUsage := "Usage: codex " + wantCommand + " --last <SESSION_ID> [PROMPT]"
			if usageErr.Usage != wantUsage {
				t.Fatalf("usage = %q, want %q", usageErr.Usage, wantUsage)
			}
		})
	}
}

// TestSessionRemovedFlagDiagnosticsLikeRust pins clap's suggestion handling for
// the spawn-history flags Rust removed from `codex fork`.
func TestSessionRemovedFlagDiagnosticsLikeRust(t *testing.T) {
	cases := []struct {
		args    []string
		message string
		usage   string
	}{
		{
			args:    []string{"fork", "--history-mode", "last-n", "thread-1"},
			message: "unexpected argument '--history-mode' found\n\n  tip: to pass '--history-mode' as a value, use '-- --history-mode'",
			usage:   "Usage: codex fork [OPTIONS] [SESSION_ID] [PROMPT]",
		},
		{
			args:    []string{"fork", "--last-n", "2", "thread-1"},
			message: "unexpected argument '--last-n' found\n\n  tip: a similar argument exists: '--last'",
			usage:   "Usage: codex fork --last [SESSION_ID] [PROMPT]",
		},
		{
			args:    []string{"fork", "--last-turn-id", "turn-2", "thread-1"},
			message: "unexpected argument '--last-turn-id' found\n\n  tip: a similar argument exists: '--last'",
			usage:   "Usage: codex fork --last [SESSION_ID] [PROMPT]",
		},
		{
			args:    []string{"fork", "--ephemeral", "thread-1"},
			message: "unexpected argument '--ephemeral' found\n\n  tip: to pass '--ephemeral' as a value, use '-- --ephemeral'",
			usage:   "Usage: codex fork [OPTIONS] [SESSION_ID] [PROMPT]",
		},
	}
	for _, testCase := range cases {
		t.Run(strings.Join(testCase.args, " "), func(t *testing.T) {
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

// TestQueueClapDiagnosticsLikeRust pins QueueCommand's declared option surface:
// clap validates required arguments first, hidden inherited options stay out of
// the smart usage block, and restricted options come back as raw clap errors.
func TestQueueClapDiagnosticsLikeRust(t *testing.T) {
	t.Run("required arguments before restrictions", func(t *testing.T) {
		usageErr := requireClapUsageError(t, []string{"queue", "-i", "a.png"})
		if usageErr.Message != "the following required arguments were not provided:\n  --thread <THREAD>\n  --message <TEXT>" {
			t.Fatalf("message = %q", usageErr.Message)
		}
		if usageErr.Usage != "Usage: codex queue --thread <THREAD> --message <TEXT>" {
			t.Fatalf("usage = %q", usageErr.Usage)
		}
	})
	t.Run("visible options stay in usage", func(t *testing.T) {
		usageErr := requireClapUsageError(t, []string{"queue", "--strict-config"})
		if usageErr.Usage != "Usage: codex queue --thread <THREAD> --message <TEXT> --strict-config" {
			t.Fatalf("usage = %q", usageErr.Usage)
		}
	})
	t.Run("restricted options are raw clap errors", func(t *testing.T) {
		for _, testCase := range []struct {
			args    []string
			message string
		}{
			{[]string{"queue", "--thread", "t", "--message", "m", "-i", "a.png"}, "`codex queue` does not support image attachments"},
			{[]string{"queue", "-m", "x", "--thread", "t", "--message", "m"}, "`codex queue` does not support `--model`; queued messages use the thread's model"},
			{[]string{"queue", "--sandbox", "read-only", "--thread", "t", "--message", "m"}, "`codex queue` does not support `--sandbox`"},
		} {
			usageErr := requireClapUsageError(t, testCase.args)
			if usageErr.Message != testCase.message {
				t.Fatalf("Parse(%v) message = %q, want %q", testCase.args, usageErr.Message, testCase.message)
			}
			if !usageErr.Raw || usageErr.Usage != "" {
				t.Fatalf("Parse(%v) error = %#v, want a raw clap error", testCase.args, usageErr)
			}
		}
	})
	t.Run("undeclared options are unexpected arguments", func(t *testing.T) {
		usageErr := requireClapUsageError(t, []string{"queue", "-a", "never"})
		if usageErr.Message != "unexpected argument '-a' found" {
			t.Fatalf("message = %q", usageErr.Message)
		}
		usageErr = requireClapUsageError(t, []string{"queue", "-aon-request"})
		if usageErr.Message != "unexpected argument '-a' found" {
			t.Fatalf("attached short flag message = %q", usageErr.Message)
		}
		usageErr = requireClapUsageError(t, []string{"queue", "--no-worktree"})
		if usageErr.Message != "unexpected argument '--no-worktree' found\n\n  tip: a similar argument exists: '--worktree'" {
			t.Fatalf("similar argument message = %q", usageErr.Message)
		}
		usageErr = requireClapUsageError(t, []string{"queue", "extra"})
		if usageErr.Message != "unexpected argument 'extra' found" {
			t.Fatalf("positional message = %q", usageErr.Message)
		}
		usageErr = requireClapUsageError(t, []string{"queue", "--", "thread"})
		if usageErr.Message != "unexpected argument 'thread' found" {
			t.Fatalf("terminator message = %q", usageErr.Message)
		}
	})
}
