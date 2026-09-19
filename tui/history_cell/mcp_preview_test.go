package historycell

import (
	"strings"
	"testing"

	"codex_go/tui"
)

// Mirrors Rust #46492: the compact code-mode preview shares one rendered-row
// budget across every result block, counts a partially displayed logical line as
// hidden, and keeps the complete result text in the transcript and raw output.
func TestMcpToolCallPreviewSharesOneBudgetLikeRust(t *testing.T) {
	cell := NewMcpToolCall("call-1", McpInvocation{Server: "node_repl", Tool: "js"}, McpToolResult{
		Content: []string{"one\ntwo\nthree", "four\nfive", "six"},
	})

	display := strings.Join(cell.DisplayLines(80), "\n")
	// Three logical lines are shown, so the remaining three are hidden.
	if !strings.Contains(display, "+3 lines ("+tui.TranscriptHint+")") {
		t.Fatalf("preview = %q, want the shared hidden-line count", display)
	}
	rows := 0
	for _, line := range strings.Split(display, "\n") {
		if strings.HasPrefix(line, "  \u2514 ") || strings.HasPrefix(line, "    ") {
			rows++
		}
	}
	if want := tui.PreviewLines + 1; rows != want {
		t.Fatalf("preview rows = %d, want %d (three preview rows plus the marker): %q", rows, want, display)
	}
	if strings.Contains(display, "five") || strings.Contains(display, "six") {
		t.Fatalf("preview kept rows past the budget: %q", display)
	}

	// The transcript and raw output keep every block, including the trailing
	// diagnostics that the preview hides.
	for name, lines := range map[string][]string{
		"transcript": cell.TranscriptLines(80),
		"raw":        cell.RawLines(),
	} {
		joined := strings.Join(lines, "\n")
		for _, want := range []string{"one", "two", "three", "four", "five", "six"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("%s dropped %q: %q", name, want, joined)
			}
		}
	}
}

// A trailing failure diagnostic is never crowded out of the transcript, even when
// the preview only shows the leading rows.
func TestMcpToolCallTranscriptKeepsTrailingFailureLikeRust(t *testing.T) {
	cell := NewMcpToolCall("call-1", McpInvocation{Server: "node_repl", Tool: "js"}, McpToolResult{
		Content: []string{"one\ntwo\nthree\nfour\nfive", "Error: script failed at line 5"},
	})
	preview := strings.Join(cell.DisplayLines(80), "\n")
	if !strings.Contains(preview, "+") || !strings.Contains(preview, tui.TranscriptHint) {
		t.Fatalf("preview = %q, want a hidden-line marker", preview)
	}
	transcript := strings.Join(cell.TranscriptLines(80), "\n")
	if !strings.Contains(transcript, "Error: script failed at line 5") {
		t.Fatalf("transcript dropped the trailing failure: %q", transcript)
	}
}
