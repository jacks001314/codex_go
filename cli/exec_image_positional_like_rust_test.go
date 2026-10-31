package cli

import (
	"errors"
	"strings"
	"testing"
)

// Rust's root/exec `--image` declares num_args = 1.., so it consumes every
// following non-flag argument; `exec resume`/`fork` declare a single value.
func TestParseExecImageIsVariadicLikeRust(t *testing.T) {
	parsed, err := Parse([]string{"exec", "-i", "a.png", "b.png"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if got := parsed.Exec.Shared.Images; len(got) != 2 || got[0] != "a.png" || got[1] != "b.png" {
		t.Fatalf("images = %#v, want both values", got)
	}
	if parsed.Exec.Prompt != "" {
		t.Fatalf("prompt = %q, want empty because the second value is an image", parsed.Exec.Prompt)
	}

	parsed, err = Parse([]string{"exec", "reply now", "-i", "a.png,b.png", "c.png"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Exec.Prompt != "reply now" {
		t.Fatalf("prompt = %q", parsed.Exec.Prompt)
	}
	if got := parsed.Exec.Shared.Images; len(got) != 3 || got[2] != "c.png" {
		t.Fatalf("images = %#v, want comma split plus the trailing value", got)
	}
}

func TestParseExecImageVariadicStopsAtOptions(t *testing.T) {
	parsed, err := Parse([]string{"exec", "-i", "a.png", "-m", "gpt-5", "hi"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if got := parsed.Exec.Shared.Images; len(got) != 1 || got[0] != "a.png" {
		t.Fatalf("images = %#v", got)
	}
	if parsed.Exec.Shared.Model != "gpt-5" || parsed.Exec.Prompt != "hi" {
		t.Fatalf("exec = %#v", parsed.Exec)
	}
}

func TestParseExecImageRequiresValueLikeRust(t *testing.T) {
	for _, args := range [][]string{{"exec", "-i"}, {"exec", "--image=", "hi"}} {
		_, err := Parse(args)
		var usageErr *UsageError
		if !errors.As(err, &usageErr) {
			t.Fatalf("Parse(%v) error = %v, want a UsageError", args, err)
		}
		want := "a value is required for '--image <FILE>...' but none was supplied"
		if usageErr.Message != want {
			t.Fatalf("Parse(%v) message = %q, want %q", args, usageErr.Message, want)
		}
	}
}

func TestParseExecResumeImageStaysSingleValueLikeRust(t *testing.T) {
	parsed, err := Parse([]string{"exec", "resume", "--last", "-i", "a.png", "tell me a joke"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if got := parsed.Exec.Shared.Images; len(got) != 1 || got[0] != "a.png" {
		t.Fatalf("resume images = %#v", got)
	}
	if parsed.Exec.Resume.Prompt != "tell me a joke" {
		t.Fatalf("resume prompt = %q", parsed.Exec.Resume.Prompt)
	}
}

// Rust declares a single PROMPT positional, so a second positional is a clap
// diagnostic without the `--` tip that unknown flags get.
func TestParseExecRejectsSecondPositionalLikeRust(t *testing.T) {
	uses := [][]string{
		{"exec", "hello", "world"},
		{"exec", "--", "hello", "world"},
		{"exec", "-", "extra"},
	}
	for _, args := range uses {
		_, err := Parse(args)
		var usageErr *UsageError
		if !errors.As(err, &usageErr) {
			t.Fatalf("Parse(%v) error = %v, want a UsageError", args, err)
		}
		if want := "unexpected argument 'world' found"; args[1] == "-" {
			if usageErr.Message != "unexpected argument 'extra' found" {
				t.Fatalf("Parse(%v) message = %q", args, usageErr.Message)
			}
		} else if usageErr.Message != want {
			t.Fatalf("Parse(%v) message = %q, want %q", args, usageErr.Message, want)
		}
		if strings.Contains(usageErr.Message, "tip:") {
			t.Fatalf("Parse(%v) message = %q, want no value tip", args, usageErr.Message)
		}
		if usageErr.Usage != execUsageText {
			t.Fatalf("Parse(%v) usage = %q", args, usageErr.Usage)
		}
	}
}

func TestParseExecPromptBeforeFlagsIsKept(t *testing.T) {
	parsed, err := Parse([]string{"exec", "reply with exactly: IMG-OK", "-i", "a.png", "b.png"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Exec.Prompt != "reply with exactly: IMG-OK" {
		t.Fatalf("prompt = %q", parsed.Exec.Prompt)
	}
	if got := parsed.Exec.Shared.Images; len(got) != 2 {
		t.Fatalf("images = %#v", got)
	}
}
