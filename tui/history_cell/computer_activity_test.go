package historycell

import (
	"strings"
	"testing"
)

func computerCall(id string, title string, result *McpToolResult) McpToolCallCell {
	arguments := ""
	if title != "" {
		arguments = `{"title":"` + title + `"}`
	}
	call := McpToolCallCell{
		CallID:     id,
		Invocation: McpInvocation{Server: ComputerActivityServer, Tool: "exec", Arguments: arguments},
	}
	if result != nil {
		call.Result = cloneMcpToolResult(result)
	}
	return call
}

// TestComputerActivityGroupsAdjacentCalls covers Rust #43576: only CUA calls
// enter the cell, replay-deduplicated, and completion-only replay completes the
// matching call.
func TestComputerActivityGroupsAdjacentCalls(t *testing.T) {
	var cell ComputerActivityCell
	cell.Start(computerCall("cua-1", "Click", nil))
	cell.Start(computerCall("cua-1", "Click", nil))
	if cell.Count() != 1 {
		t.Fatalf("dedup count = %d, want 1", cell.Count())
	}
	if !cell.IsActive() {
		t.Fatal("a call without a result keeps the group active")
	}
	cell.Complete(computerCall("cua-1", "Click", nil), McpToolResult{Content: []string{"ok"}})
	cell.Complete(computerCall("cua-2", "Type", nil), McpToolResult{Content: []string{"ok"}})
	if cell.Count() != 2 || cell.IsActive() {
		t.Fatalf("cell = %#v", cell)
	}

	width := 80
	lines := cell.DisplayLines(width)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Used computer") || !strings.Contains(joined, "2 actions") {
		t.Fatalf("header missing:\n%s", joined)
	}
	if !strings.Contains(joined, "Click") || !strings.Contains(joined, "Type") {
		t.Fatalf("rows missing:\n%s", joined)
	}
	if !IsComputerActivityServer(" cua_repl ") || IsComputerActivityServer("node_repl") {
		t.Fatal("server detection must match cua_repl exactly")
	}
}

// TestComputerActivityActiveAndFailurePreviews covers the active header, the
// failure count/summary, the screenshot summary, and the hidden-rows hint.
func TestComputerActivityActiveAndFailurePreviews(t *testing.T) {
	var active ComputerActivityCell
	active.Start(computerCall("cua-1", "Click", nil))
	joined := strings.Join(active.DisplayLines(80), "\n")
	if !strings.Contains(joined, "Using computer") || !strings.Contains(joined, "1 action") {
		t.Fatalf("active header missing:\n%s", joined)
	}

	var failed ComputerActivityCell
	failed.Complete(computerCall("cua-1", "Click", nil), McpToolResult{Error: "Script error: boom\nat x", IsError: true})
	failed.Complete(computerCall("cua-2", "Type", nil), McpToolResult{Content: []string{"captured"}, HasImage: true})
	failed.Complete(computerCall("cua-3", "Scroll", nil), McpToolResult{Content: []string{"ok"}})
	failed.Complete(computerCall("cua-4", "Wait", nil), McpToolResult{Content: []string{"ok"}})
	joined = strings.Join(failed.DisplayLines(80), "\n")
	if !strings.Contains(joined, "1 failed") {
		t.Fatalf("failure count missing:\n%s", joined)
	}
	if !strings.Contains(joined, "Failed: Click \u2014 boom") {
		t.Fatalf("failure preview missing:\n%s", joined)
	}
	if !strings.Contains(joined, "Captured screenshot \u00b7 Type") {
		t.Fatalf("screenshot preview missing:\n%s", joined)
	}
	if !strings.Contains(joined, "2 more \u00b7 ctrl+t") {
		t.Fatalf("hidden-rows hint missing:\n%s", joined)
	}

	// A missing argument title falls back to "Computer action".
	var untitled ComputerActivityCell
	untitled.Complete(computerCall("cua-1", "", nil), McpToolResult{Content: []string{"ok"}})
	if joined := strings.Join(untitled.DisplayLines(80), "\n"); !strings.Contains(joined, "Computer action") {
		t.Fatalf("title fallback missing:\n%s", joined)
	}
}

// TestComputerActivityTranscriptAndInterruption covers the full transcript and
// the turn-end failure path.
func TestComputerActivityTranscriptAndInterruption(t *testing.T) {
	var cell ComputerActivityCell
	cell.Complete(computerCall("cua-1", "Click", nil), McpToolResult{Content: []string{"line one\nline two"}})
	transcript := strings.Join(cell.TranscriptLines(120), "\n")
	if !strings.Contains(transcript, "line one") || !strings.Contains(transcript, "Click") {
		t.Fatalf("transcript missing detail:\n%s", transcript)
	}
	if len(cell.RawLines()) == 0 {
		t.Fatal("raw lines must not be empty")
	}

	cell.Start(computerCall("cua-2", "Type", nil))
	if !cell.IsActive() {
		t.Fatal("running call must keep the group active")
	}
	cell.MarkFailed("Interrupted current turn.")
	if cell.IsActive() {
		t.Fatal("mark failed must complete the running calls")
	}
	if joined := strings.Join(cell.DisplayLines(80), "\n"); !strings.Contains(joined, "Failed: Type") {
		t.Fatalf("interrupted call should preview as failed:\n%s", joined)
	}
}
