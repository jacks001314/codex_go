package tui

import (
	"strings"
	"testing"
	"time"
)

func TestStatusIndicatorWidgetElapsedFormattingMatchesRust(t *testing.T) {
	cases := map[uint64]string{
		0:             "0s",
		59:            "59s",
		60:            "1m 00s",
		61:            "1m 01s",
		59*60 + 59:    "59m 59s",
		3600:          "1h 00m 00s",
		25*3600 + 123: "25h 02m 03s",
	}
	for input, want := range cases {
		if got := FmtElapsedCompact(input); got != want {
			t.Fatalf("FmtElapsedCompact(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestStatusIndicatorWidgetStatePauseResumeMatchesRust(t *testing.T) {
	start := time.Unix(100, 0)
	widget := NewStatusIndicatorWidgetState(start)
	widget.PauseAt(start.Add(5 * time.Second))
	if got := widget.Elapsed(start.Add(20 * time.Second)); got != 5*time.Second {
		t.Fatalf("paused elapsed = %v", got)
	}
	widget.ResumeAt(start.Add(20 * time.Second))
	if got := widget.Elapsed(start.Add(23 * time.Second)); got != 8*time.Second {
		t.Fatalf("resumed elapsed = %v", got)
	}
}

func TestStatusIndicatorWidgetStateDetailsAndInlineMatchRust(t *testing.T) {
	start := time.Unix(0, 0)
	widget := NewStatusIndicatorWidgetState(start)
	widget.UpdateHeader("thinking")
	widget.UpdateDetails("  cargo test -p codex-core and then cargo test -p codex-tui", StatusDetailsCapitalizeFirst, 1)
	widget.UpdateInlineMessage("  running tests  ")
	lines := widget.Render(80, start.Add(61*time.Second))
	if len(lines) != 2 {
		t.Fatalf("lines = %#v", lines)
	}
	if !strings.Contains(lines[0], "thinking (1m 01s") || !strings.Contains(lines[0], "running tests") {
		t.Fatalf("header line = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  - Cargo") {
		t.Fatalf("details line = %q", lines[1])
	}
}

func TestStatusIndicatorWidgetInterruptHintToggle(t *testing.T) {
	start := time.Unix(0, 0)
	widget := NewStatusIndicatorWidgetState(start)
	widget.SetInterruptHintVisible(false)
	lines := widget.Render(80, start)
	if len(lines) == 0 || strings.Contains(lines[0], "to interrupt") {
		t.Fatalf("interrupt hint should be hidden: %#v", lines)
	}
}
