package cli

import (
	"slices"
	"strings"
	"testing"
)

// TestExecTrustFlagSurfaceLikeRust freezes where Rust declares `--trust`
// (#60041): the exec Cli marks it global, so `codex exec`, `codex exec review`,
// `codex exec resume` and `codex exec fork` all accept it, and ReviewCommand
// declares its own copy for the top-level `codex review`.
func TestExecTrustFlagSurfaceLikeRust(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "exec", args: []string{"exec", "--trust", "hi"}},
		{name: "exec before prompt", args: []string{"exec", "hi", "--trust"}},
		{name: "exec review", args: []string{"exec", "review", "--trust", "--uncommitted"}},
		{name: "exec resume", args: []string{"exec", "resume", "--trust", "--last"}},
		{name: "exec fork", args: []string{"exec", "fork", "--trust", "session-1"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := Parse(testCase.args)
			if err != nil {
				t.Fatalf("Parse(%v) returned error: %v", testCase.args, err)
			}
			if !parsed.Exec.Trust {
				t.Fatalf("Parse(%v).Exec.Trust = false, want true", testCase.args)
			}
			if parsed.Exec.Review.Trust {
				t.Fatalf("Parse(%v).Exec.Review.Trust = true, want the exec copy only", testCase.args)
			}
		})
	}

	parsed, err := Parse([]string{"review", "--trust", "--uncommitted"})
	if err != nil {
		t.Fatalf("Parse(review --trust) returned error: %v", err)
	}
	if !parsed.Exec.Review.Trust {
		t.Fatal("Parse(review --trust).Exec.Review.Trust = false, want true")
	}
	if parsed.Exec.Trust {
		t.Fatal("Parse(review --trust).Exec.Trust = true, want the review copy only")
	}

	// Without the flag the option stays false, so nothing changes for existing
	// invocations.
	parsed, err = Parse([]string{"exec", "hi"})
	if err != nil {
		t.Fatalf("Parse(exec hi) returned error: %v", err)
	}
	if parsed.Exec.Trust {
		t.Fatal("Parse(exec hi).Exec.Trust = true, want false")
	}
}

// TestExecTrustFlagRejectsRepeatsLikeRust keeps clap's SetTrue diagnostic: a
// repeated boolean is rejected rather than silently accepted.
func TestExecTrustFlagRejectsRepeatsLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"exec", "--trust", "--trust", "hi"},
		{"review", "--trust", "--trust"},
	} {
		_, err := Parse(args)
		if err == nil {
			t.Fatalf("Parse(%v) succeeded, want clap's repeat diagnostic", args)
		}
		if !strings.Contains(err.Error(), "cannot be used multiple times") {
			t.Fatalf("Parse(%v) error = %v, want the repeat diagnostic", args, err)
		}
	}
}

// TestExecTrustAppearsInClapSurfacesLikeRust keeps the four clap surfaces that
// list exec options in sync: the long help pages, the short help pages, the
// smart-usage flag table and shell completion.
func TestExecTrustAppearsInClapSurfacesLikeRust(t *testing.T) {
	for _, page := range []struct {
		kind  ExecHelpKind
		short bool
	}{
		{kind: ExecHelpExec},
		{kind: ExecHelpExec, short: true},
		{kind: ExecHelpResume},
		{kind: ExecHelpResume, short: true},
		{kind: ExecHelpFork},
		{kind: ExecHelpFork, short: true},
		{kind: ExecHelpReview},
		{kind: ExecHelpReview, short: true},
	} {
		text := ExecHelpText(page.kind, page.short)
		if !strings.Contains(text, "--trust") {
			t.Fatalf("exec help page %v (short=%v) does not list --trust", page.kind, page.short)
		}
		if !strings.Contains(text, "Save trust for this project and future chats") {
			t.Fatalf("exec help page %v (short=%v) is missing the --trust description", page.kind, page.short)
		}
	}
	for _, command := range []string{"exec", "exec resume", "exec fork", "exec review", "review"} {
		if !clapCommandDeclaresLongFlag(command, "--trust") {
			t.Fatalf("clapCommandDeclaresLongFlag(%q, --trust) = false", command)
		}
	}
	for _, command := range []string{"resume", "fork", "sandbox", "mcp"} {
		if clapCommandDeclaresLongFlag(command, "--trust") {
			t.Fatalf("clapCommandDeclaresLongFlag(%q, --trust) = true, want false", command)
		}
	}
	if !slices.Contains(execCompletionOptions(), "--trust") {
		t.Fatal("execCompletionOptions() is missing --trust")
	}
	if !slices.Contains(reviewCompletionOptions(), "--trust") {
		t.Fatal("reviewCompletionOptions() is missing --trust")
	}
}
