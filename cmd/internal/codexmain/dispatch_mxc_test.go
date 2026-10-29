package codexmain

import (
	"bytes"
	"strings"
	"testing"

	"codex_go/sandbox/mxc"
)

// Mirrors Rust #44903: `--__codex-windows-mxc` is dispatched before ordinary
// CLI parsing, so the helper never reaches argument parsing or path aliasing.
func TestDispatchInternalHelperSelectsWindowsMXCLikeRust(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code, handled := dispatchInternalHelper(
		"codex",
		mxc.LauncherArg1,
		[]string{mxc.LauncherArg1},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if !handled {
		t.Fatal("dispatchInternalHelper() did not handle the MXC launcher argument")
	}
	if code != 1 {
		t.Fatalf("dispatchInternalHelper() = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	// The helper never falls through to CLI help or usage output.
	if got := stderr.String(); !strings.Contains(got, "MXC") || strings.Contains(got, "Usage:") {
		t.Fatalf("stderr = %q", got)
	}
}

func TestDispatchInternalHelperLeavesOrdinaryInvocationsAlone(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if _, handled := dispatchInternalHelper(
		"codex",
		"exec",
		[]string{"exec", "hello"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	); handled {
		t.Fatal("dispatchInternalHelper() handled an ordinary CLI invocation")
	}
}
