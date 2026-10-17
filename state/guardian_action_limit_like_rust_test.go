package state

import (
	"strings"
	"testing"
)

// Mirrors Rust's guardian_action_formatters_reject_large_aggregate_payloads
// (#44060): a payload that only becomes too large in aggregate is rejected
// outright instead of being reviewed partially.
func TestGuardianActionReviewLimitRejectsLargeAggregatePayloadsLikeRust(t *testing.T) {
	if GuardianMaxActionBytes != 200_000 {
		t.Fatalf("GuardianMaxActionBytes = %d, want 200000", GuardianMaxActionBytes)
	}
	files := make([]string, 20_000)
	for index := range files {
		files[index] = "/tmp/file"
	}
	_, _, err := FormatGuardianActionPretty(Action{Type: "apply_patch", CWD: "/tmp", Files: files})
	if err == nil {
		t.Fatal("FormatGuardianActionPretty() error = nil, want the review-limit failure")
	}
	if got, want := err.Error(), "Guardian action exceeds the 200000-byte review limit"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

// Rust raised the action review limit to 200,000 bytes (#44060), so MCP
// arguments above the former 8,000-byte bound stay fully reviewable.
func TestGuardianActionReviewLimitAcceptsFormerlyOversizedArgumentsLikeRust(t *testing.T) {
	arguments := map[string]any{"message": strings.Repeat("review", 1_500)}
	text, truncated, err := FormatGuardianActionPretty(Action{
		Type:      "mcp_tool_call",
		Server:    "server",
		ToolName:  "tool",
		Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("FormatGuardianActionPretty() error = %v, want the action accepted", err)
	}
	if truncated {
		t.Fatal("FormatGuardianActionPretty() truncated an action whose strings fit the per-string budget")
	}
	if len(text) <= 8_000 {
		t.Fatalf("rendered action is only %d bytes; the fixture must exceed the former 8,000-byte limit", len(text))
	}
	if !strings.Contains(text, strings.Repeat("review", 1_500)) {
		t.Fatal("the full MCP argument was not preserved in the reviewed action")
	}
}

// Mirrors Rust's guardian_truncate_text_keeps_prefix_suffix_and_xml_marker via
// truncate_guardian_action_value: one oversized string keeps both ends and an
// explicit omission marker instead of failing the whole review.
func TestGuardianActionReviewLimitTruncatesOversizedStringsLikeRust(t *testing.T) {
	chars := "prefix " + strings.Repeat("middle ", 20_000) + "suffix"
	text, truncated, err := FormatGuardianActionPretty(Action{
		Type:      "write_stdin",
		SessionID: intPointer(7),
		Chars:     chars,
	})
	if err != nil {
		t.Fatalf("FormatGuardianActionPretty() error = %v, want the oversized string truncated", err)
	}
	if !truncated {
		t.Fatal("FormatGuardianActionPretty() reported no truncation for an oversized string")
	}
	if len(text) > GuardianMaxActionBytes {
		t.Fatalf("truncated action is %d bytes, want at most %d", len(text), GuardianMaxActionBytes)
	}
	if !strings.Contains(text, "<truncated omitted_approx_tokens=") {
		t.Fatal("truncated action is missing the shared Guardian omission marker")
	}
	if !strings.Contains(text, "prefix ") || !strings.Contains(text, "suffix") {
		t.Fatal("truncated action lost its prefix or suffix evidence")
	}
}

func intPointer(value int) *int {
	return &value
}
