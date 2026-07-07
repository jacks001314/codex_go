package shell

import "testing"

func TestCanonicalizeShellLCPlainCommand(t *testing.T) {
	got := CanonicalizeForApproval([]string{"/bin/bash", "-lc", "git status --short"})
	want := []string{"git", "status", "--short"}
	assertStrings(t, got, want)
}

func TestCanonicalizeBashScriptWhenComplex(t *testing.T) {
	got := CanonicalizeForApproval([]string{"bash", "-lc", "echo hi && rm -rf tmp"})
	want := []string{CanonicalBashScriptPrefix, "-lc", "echo hi && rm -rf tmp"}
	assertStrings(t, got, want)
}

func TestCanonicalizePowerShellScript(t *testing.T) {
	got := CanonicalizeForApproval([]string{"powershell.exe", "-NoProfile", "-Command", "Get-ChildItem"})
	want := []string{CanonicalPowerShellScriptPrefix, "Get-ChildItem"}
	assertStrings(t, got, want)
}

func TestCanonicalizeReturnsCopyForUnknownCommand(t *testing.T) {
	input := []string{"git", "status"}
	got := CanonicalizeForApproval(input)
	got[0] = "mutated"
	if input[0] != "git" {
		t.Fatalf("CanonicalizeForApproval leaked input mutation")
	}
}

func TestSplitPlainShellQuotes(t *testing.T) {
	got := splitPlainShell(`git commit -m "hello world"`)
	want := []string{"git", "commit", "-m", "hello world"}
	assertStrings(t, got, want)
}

func assertStrings(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d: got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q; got=%v want=%v", i, got[i], want[i], got, want)
		}
	}
}
