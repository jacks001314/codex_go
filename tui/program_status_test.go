package tui

import (
	"strings"
	"testing"
)

// TestProgramStatusSequencesLikeRust mirrors Rust #54114
// terminal_program_status.rs: OSC 7501 reports done/error and the blocking
// kind, and OSC 21337 keeps the legacy labels with done/error mapped to Idle.
func TestProgramStatusSequencesLikeRust(t *testing.T) {
	cases := []struct {
		status ProgramStatus
		want   string
	}{
		{ProgramStatus{}, "\x1b]7501;state=idle:app=codex\x1b\\"},
		{ProgramStatus{State: ProgramStatusWorking}, "\x1b]7501;state=working:app=codex\x1b\\"},
		{ProgramStatus{State: ProgramStatusDone}, "\x1b]7501;state=done:app=codex\x1b\\"},
		{ProgramStatus{State: ProgramStatusError}, "\x1b]7501;state=error:app=codex\x1b\\"},
		{BlockedProgramStatus(ProgramStatusKindPermission), "\x1b]7501;state=blocked:app=codex:kind=permission\x1b\\"},
		{BlockedProgramStatus(ProgramStatusKindQuestion), "\x1b]7501;state=blocked:app=codex:kind=question\x1b\\"},
		{BlockedProgramStatus(ProgramStatusKindAuth), "\x1b]7501;state=blocked:app=codex:kind=auth\x1b\\"},
	}
	for _, tc := range cases {
		if got := SetTerminalProgramStatusSequence(tc.status, "", false); got != tc.want {
			t.Fatalf("sequence for %v = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// TestITermProgramStatusLabelsLikeRust pins the preserved OSC 21337 surface.
func TestITermProgramStatusLabelsLikeRust(t *testing.T) {
	done := SetTerminalProgramStatusSequence(ProgramStatus{State: ProgramStatusDone}, "Detail", true)
	wantPrefix := "\x1b]21337;status=Idle;indicator=#00d75f;status-color=;detail=Detail\x07"
	if !strings.HasPrefix(done, wantPrefix) {
		t.Fatalf("done sequence = %q, want prefix %q", done, wantPrefix)
	}
	if !strings.HasSuffix(done, "\x1b]7501;state=done:app=codex\x1b\\") {
		t.Fatalf("done sequence = %q, want the OSC 7501 tail", done)
	}
	blocked := SetTerminalProgramStatusSequence(BlockedProgramStatus(ProgramStatusKindAuth), "", true)
	if !strings.HasPrefix(blocked, "\x1b]21337;status=Waiting;indicator=#5f87ff;status-color=;detail=\x07") {
		t.Fatalf("blocked sequence = %q, want the Waiting label", blocked)
	}
	if got := ClearTerminalProgramStatusSequence(true); got != "\x1b]21337;status=;indicator=;status-color=;detail=\x07\x1b]7501;state=clear\x1b\\" {
		t.Fatalf("clear sequence = %q", got)
	}
	if got := ClearTerminalProgramStatusSequence(false); got != "\x1b]7501;state=clear\x1b\\" {
		t.Fatalf("plain clear sequence = %q", got)
	}
}

// TestITermSessionDetailSanitizationLikeRust mirrors
// sanitize_iterm_session_detail: control characters are stripped, the title
// limit applies, and ';' and '\\' are escaped so they cannot break the
// OSC 21337 payload.
func TestITermSessionDetailSanitizationLikeRust(t *testing.T) {
	got := sanitizeITermSessionDetail("  Read a\\b; c\n\u202e\x1b  ")
	want := "Read a\\\\b\\; c"
	if got != want {
		t.Fatalf("sanitize = %q, want %q", got, want)
	}
	if got := sanitizeITermSessionDetail(strings.Repeat("x", 300)); len([]rune(got)) != MaxTerminalTitleChars {
		t.Fatalf("long detail length = %d, want %d", len([]rune(got)), MaxTerminalTitleChars)
	}
}

// TestProgramStatusCacheSuppressesDuplicatesLikeRust pins the process-wide
// cache: identical states are not re-emitted, invalidation forces a re-emit,
// and clearing twice emits once.
func TestProgramStatusCacheSuppressesDuplicatesLikeRust(t *testing.T) {
	restore := SetTerminalProgramStatusSupportForTest(true, true)
	defer restore()

	working := ProgramStatus{State: ProgramStatusWorking}
	if got := SetTerminalProgramStatus(working, ""); got == "" {
		t.Fatal("first report was suppressed")
	}
	if got := SetTerminalProgramStatus(working, ""); got != "" {
		t.Fatalf("duplicate report emitted %q", got)
	}
	if got := SetTerminalProgramStatus(working, "Reading source"); got == "" {
		t.Fatal("changed activity detail was suppressed")
	}
	if got := SetTerminalProgramStatus(working, "Reading source"); got != "" {
		t.Fatalf("duplicate activity detail emitted %q", got)
	}
	if got := SetTerminalProgramStatus(ProgramStatus{State: ProgramStatusDone}, ""); got == "" {
		t.Fatal("changed status was suppressed")
	}
	InvalidateTerminalProgramStatus()
	if got := SetTerminalProgramStatus(ProgramStatus{State: ProgramStatusDone}, ""); got == "" {
		t.Fatal("invalidated status was not re-emitted")
	}
	if got := ClearTerminalProgramStatus(); got == "" {
		t.Fatal("clear emitted nothing")
	}
	if got := ClearTerminalProgramStatus(); got != "" {
		t.Fatalf("second clear emitted %q", got)
	}
}
