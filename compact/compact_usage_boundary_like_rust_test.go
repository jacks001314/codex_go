package compact

import (
	"encoding/json"
	"strings"
	"testing"
)

func usageBoundaryReasoningItem(size int) Item {
	raw, err := json.Marshal(map[string]any{"type": "reasoning", "summary": []any{}, "encrypted_content": strings.Repeat("A", size)})
	if err != nil {
		panic(err)
	}
	return Item{ID: "r1", Type: "reasoning", Raw: raw}
}

// Mirrors Rust #58404 history::items_since_last_usage: only history at or after
// the boundary covered by the last usable usage report is re-estimated.
func TestItemsSinceLastUsageLikeRust(t *testing.T) {
	items := []Item{
		{ID: "u1", Type: "message", Role: "user", Text: "first"},
		{ID: "a1", Type: "message", Role: "assistant", Text: "answer"},
		{ID: "u2", Type: "message", Role: "user", Text: "next prompt"},
	}
	if got := ItemsSinceLastUsage(items, 2); len(got) != 1 || got[0].ID != "u2" {
		t.Fatalf("items since usage boundary 2 = %+v", got)
	}
	if got := ItemsSinceLastUsage(items, len(items)); len(got) != 0 {
		t.Fatalf("items since covering boundary = %+v, want none", got)
	}
	if got := ItemsSinceLastUsage(items, -1); len(got) != 1 || got[0].ID != "u2" {
		t.Fatalf("items since unknown boundary = %+v, want the restored approximation", got)
	}
}

// Mirrors Rust #58404: reasoning after the usage boundary is already charged by
// the trailing-item estimate, so it must not be counted twice.
func TestEstimateActiveContextTokensHonorsUsageBoundaryLikeRust(t *testing.T) {
	items := []Item{
		{ID: "u1", Type: "message", Role: "user", Text: "first"},
		{ID: "a1", Type: "message", Role: "assistant", Text: "answer"},
		usageBoundaryReasoningItem(4000),
		{ID: "u2", Type: "message", Role: "user", Text: "next prompt"},
	}
	if EstimateNonLastReasoningTokens(items) <= 0 {
		t.Fatalf("unbounded reasoning estimate must charge the encrypted payload")
	}
	boundary := 2
	if got := EstimateNonLastReasoningTokensSinceUsage(items, boundary); got != 0 {
		t.Fatalf("reasoning since usage = %d, want 0 (tail already charges it)", got)
	}
	tail := EstimateTokens(ItemsSinceLastUsage(items, boundary))
	got := EstimateActiveContextTokensWithUsageBoundary(items, 80, false, boundary)
	if want := 80 + tail; got != want {
		t.Fatalf("active tokens with boundary = %d, want %d", got, want)
	}
}

// Mirrors Rust #58404: an empty successful response still covers the request
// input, so nothing beyond the boundary is estimated.
func TestEstimateActiveContextTokensCoversEmptyResponseInputLikeRust(t *testing.T) {
	items := []Item{
		{ID: "u1", Type: "message", Role: "user", Text: "first"},
		{ID: "a1", Type: "message", Role: "assistant", Text: "answer"},
	}
	if got := EstimateActiveContextTokensWithUsageBoundary(items, 100, true, len(items)); got != 100 {
		t.Fatalf("active tokens covering the request input = %d, want 100", got)
	}
}
