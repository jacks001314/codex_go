package tui

import "testing"

// Mirrors Rust #48118's three-state VS Code detection.
func TestVscodeTerminalDetectionIsConclusiveOnTermProgram(t *testing.T) {
	// Rust's term_program_is_vscode compares case-insensitively without
	// trimming, so a surrounding space is not a VS Code reading.
	for _, value := range []string{"vscode", "VSCODE", "VsCode"} {
		if got := VscodeTerminalDetection(&value, VscodeDetectionUnknown); got != VscodeDetectionVsCode {
			t.Fatalf("TERM_PROGRAM=%q detection = %q, want vscode", value, got)
		}
	}
	other := "iTerm.app"
	if got := VscodeTerminalDetection(&other, VscodeDetectionOther); got != VscodeDetectionOther {
		t.Fatalf("non-vscode TERM_PROGRAM detection = %q, want other", got)
	}
	if got := VscodeTerminalDetection(nil, VscodeDetectionUnknown); got != VscodeDetectionUnknown {
		t.Fatalf("unset TERM_PROGRAM detection = %q, want unknown", got)
	}
}

func TestWindowsVscodeDetectionMirrorsRustProbeOutcomes(t *testing.T) {
	probeCalled := false
	probe := func() (string, bool, bool) {
		probeCalled = true
		return "vscode", true, true
	}

	// Only a Linux host can see the Windows-side variable, and a non-WSL Linux
	// host is conclusively `other` without running the probe.
	if got := WindowsVscodeDetection("darwin", true, probe); got != VscodeDetectionUnknown {
		t.Fatalf("darwin detection = %q, want unknown", got)
	}
	if got := WindowsVscodeDetection("linux", false, probe); got != VscodeDetectionOther {
		t.Fatalf("non-WSL linux detection = %q, want other", got)
	}
	if probeCalled {
		t.Fatal("the Windows probe must not run without WSL")
	}

	if got := WindowsVscodeDetection("linux", true, probe); got != VscodeDetectionVsCode {
		t.Fatalf("WSL vscode detection = %q, want vscode", got)
	}
	if got := ReadWindowsVscodeDetection(func() (string, bool, bool) { return "", false, true }); got != VscodeDetectionOther {
		t.Fatalf("absent TERM_PROGRAM detection = %q, want other", got)
	}
	if got := ReadWindowsVscodeDetection(func() (string, bool, bool) { return "", false, false }); got != VscodeDetectionUnknown {
		t.Fatalf("failed probe detection = %q, want unknown", got)
	}
	if got := ReadWindowsVscodeDetection(nil); got != VscodeDetectionUnknown {
		t.Fatalf("missing probe detection = %q, want unknown", got)
	}
}

func TestDetectVscodeTerminalReadsTermProgram(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "vscode")
	if got := DetectVscodeTerminal(); got != VscodeDetectionVsCode {
		t.Fatalf("TERM_PROGRAM=vscode detection = %q, want vscode", got)
	}
}
