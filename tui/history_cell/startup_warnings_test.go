package historycell

import (
	"reflect"
	"strings"
	"testing"
)

// Mirrors Rust's startup_warnings_wait_for_splash_and_coalesce_with_full_details
// and mcp_startup_summary_counts_servers_and_sign_in_subset after #46751: the
// retained diagnostics render no live rows, the footer and the f2 viewer own the
// count, and the transcript keeps every diagnostic.
func TestStartupWarningsRetainedWithoutLiveRowsLikeRust(t *testing.T) {
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
	if display := mixed.DisplayLines(100); len(display) != 0 {
		t.Fatalf("DisplayLines() = %#v, want none", display)
	}

	// A single warning stays out of the live transcript too.
	single := NewStartupWarnings([]string{"One"})
	if got := single.DisplayLines(100); len(got) != 0 {
		t.Fatalf("DisplayLines() = %#v, want none", got)
	}
	// Both with two sources (one MCP, one other) keeps both identities for the
	// warnings viewer.
	two := NewStartupWarnings([]string{"other"}).Merge(NewMCPStartupWarnings([]string{"mcp"}, []string{"alpha"}, ""))
	if got := len(two.WarningKeys()); got != 2 {
		t.Fatalf("WarningKeys() = %d, want 2", got)
	}
	// All-MCP sources keep one identity per failed server.
	mcpOnly := NewMCPStartupWarnings([]string{"mcp"}, []string{"alpha", "beta"}, "")
	if got := len(mcpOnly.WarningKeys()); got != 2 {
		t.Fatalf("WarningKeys() = %d, want 2", got)
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
	if got := cell.DisplayLines(100); len(got) != 0 {
		t.Fatalf("DisplayLines() = %#v, want none", got)
	}
}
