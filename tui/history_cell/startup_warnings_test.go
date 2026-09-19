package historycell

import (
	"reflect"
	"strings"
	"testing"
)

// Mirrors Rust's startup_warnings_wait_for_splash_and_coalesce_with_full_details
// and mcp_startup_summary_counts_servers_and_sign_in_subset.
func TestStartupWarningsSummaryLikeRust(t *testing.T) {
	// A pending header renders nothing, exactly like Rust before the splash.
	pending := NewStartupWarnings([]string{"Skill manifest is invalid."})
	pending.PendingHeader = true
	if lines := pending.DisplayLines(100); len(lines) != 0 {
		t.Fatalf("pending DisplayLines() = %#v, want none", lines)
	}

	mixed := NewStartupWarnings([]string{"Skill manifest is invalid."}).Merge(
		NewMCPStartupWarnings(
			[]string{"MCP alpha: connection unavailable", "MCP startup incomplete (failed: alpha, beta, gamma)"},
			[]string{"alpha", "beta", "gamma"},
			"",
		),
	)
	mixed = mixed.Merge(NewMCPStartupWarnings(nil, []string{"alpha", "beta"}, MCPStartupFailureReauthenticationRequired))
	mixed.TranscriptHint = "ctrl+t"
	display := mixed.DisplayLines(100)
	if len(display) != 1 || display[0] != "\u26a0 4 startup issues (3 MCP; 2 need sign-in) \u00b7 ctrl+t for details" {
		t.Fatalf("DisplayLines() = %#v", display)
	}

	// A single warning uses the singular wording.
	single := NewStartupWarnings([]string{"One"})
	if got := single.DisplayLines(100); len(got) != 1 || got[0] != "\u26a0 1 startup issue" {
		t.Fatalf("DisplayLines() = %#v", got)
	}
	// Both with two sources (one MCP, one other) keeps the breakdown.
	two := NewStartupWarnings([]string{"other"}).Merge(NewMCPStartupWarnings([]string{"mcp"}, []string{"alpha"}, ""))
	if got := two.DisplayLines(100); len(got) != 1 || got[0] != "\u26a0 2 startup issues (1 MCP)" {
		t.Fatalf("DisplayLines() = %#v", got)
	}
	// All-MCP sources take the MCP prefix.
	mcpOnly := NewMCPStartupWarnings([]string{"mcp"}, []string{"alpha", "beta"}, "")
	if got := mcpOnly.DisplayLines(100); len(got) != 1 || got[0] != "\u26a0 2 MCP startup issues" {
		t.Fatalf("DisplayLines() = %#v", got)
	}
	// The transcript keeps the individual diagnostics with their prefix.
	transcript := strings.Join(mixed.TranscriptLines(80), "\n")
	if !strings.Contains(transcript, "\u26a0 Skill manifest is invalid.") ||
		!strings.Contains(transcript, "\u26a0 MCP startup incomplete (failed: alpha, beta, gamma)") {
		t.Fatalf("TranscriptLines() = %q", transcript)
	}
	// Raw lines carry the messages without prefix or style.
	if got := mixed.RawLines(); !reflect.DeepEqual(got, mixed.Messages) {
		t.Fatalf("RawLines() = %#v, want the messages", got)
	}
}

// Mirrors Rust's coalescing: merging the same message twice does not
// double-count it, and repeated merges stay stable.
func TestStartupWarningsMergeDeduplicatesLikeRust(t *testing.T) {
	cell := NewStartupWarnings([]string{"First warning"})
	cell = cell.Merge(NewStartupWarnings([]string{"First warning", "Second warning"}))
	cell = cell.Merge(NewStartupWarnings([]string{"Second warning"}))
	if !reflect.DeepEqual(cell.Messages, []string{"First warning", "Second warning"}) {
		t.Fatalf("Messages = %#v", cell.Messages)
	}
	if len(cell.OtherSources) != 2 {
		t.Fatalf("OtherSources = %#v", cell.OtherSources)
	}
	if got := cell.DisplayLines(100); len(got) != 1 || got[0] != "\u26a0 2 startup issues" {
		t.Fatalf("DisplayLines() = %#v", got)
	}
}
