package appserver

import (
	"fmt"
	"strings"
	"testing"

	"codex_go/session"
)

// TestGuardianReviewHistoryBoundsKeepKindsSeparate mirrors Rust #41846: the
// retained review history bounds user messages and other transcript items
// independently, so a flood of tool traffic cannot evict user instructions.
func TestGuardianReviewHistoryBoundsKeepKindsSeparate(t *testing.T) {
	state := newGuardianReviewHistoryState(nil)
	state.append([]session.Item{{
		ID: "restriction", Type: "message", Role: "user", Text: "Only publish privately.",
	}})
	for index := 0; index < 300; index++ {
		state.append([]session.Item{{
			ID:     fmt.Sprintf("tool-%d", index),
			Type:   "function_call_output",
			CallID: fmt.Sprintf("call-%d", index),
			Text:   fmt.Sprintf("tool output %d", index),
		}})
	}

	var users, others int
	for _, item := range state.items {
		if guardianReviewHistoryUserKind(item) {
			users++
			if item.Text != "Only publish privately." {
				t.Fatalf("user evidence changed: %q", item.Text)
			}
			continue
		}
		others++
	}
	if users != 1 {
		t.Fatalf("retained user items = %d, want the instruction kept", users)
	}
	if others > maxGuardianReviewItemsPerKind {
		t.Fatalf("retained non-user items = %d, want at most %d", others, maxGuardianReviewItemsPerKind)
	}
	if state.items[len(state.items)-1].ID != "tool-299" {
		t.Fatalf("newest tool output = %q, want tool-299", state.items[len(state.items)-1].ID)
	}
}

// TestGuardianReviewHistoryByteBudgetEvictsOldestSameKind mirrors Rust #41846's
// per-kind byte budget: oversized tool traffic evicts older tool evidence only,
// leaving earlier user instructions in place.
func TestGuardianReviewHistoryByteBudgetEvictsOldestSameKind(t *testing.T) {
	state := newGuardianReviewHistoryState([]session.Item{{
		ID: "restriction", Type: "message", Role: "user", Text: "Only publish privately.",
	}})
	const payloadSize = 1 << 20
	for index := 0; index < 6; index++ {
		state.append([]session.Item{{
			ID:     fmt.Sprintf("tool-%d", index),
			Type:   "function_call_output",
			CallID: fmt.Sprintf("call-%d", index),
			Text:   strings.Repeat("x", payloadSize),
		}})
	}

	var others int
	var firstRetained string
	for _, item := range state.items {
		if guardianReviewHistoryUserKind(item) {
			continue
		}
		others++
		if firstRetained == "" {
			firstRetained = item.ID
		}
	}
	if others >= 6 {
		t.Fatalf("retained non-user items = %d, want the byte budget to evict older evidence", others)
	}
	if others == 0 || firstRetained == "tool-0" {
		t.Fatalf("byte eviction kept %d tool outputs starting at %q", others, firstRetained)
	}
	if state.items[0].ID != "restriction" {
		t.Fatalf("first retained item = %q, want the user instruction", state.items[0].ID)
	}
}

// TestGuardianReviewHistoryBatchesNonUserEvictionLikeRust mirrors Rust #43798:
// non-user overflow drops the oldest half of the retained entries so following
// appends do not evict again and invalidate Guardian's transcript cursor.
func TestGuardianReviewHistoryBatchesNonUserEvictionLikeRust(t *testing.T) {
	state := newGuardianReviewHistoryState(nil)
	for index := 0; index < maxGuardianReviewItemsPerKind+1; index++ {
		state.append([]session.Item{{
			ID:     fmt.Sprintf("tool-%d", index),
			Type:   "function_call_output",
			CallID: fmt.Sprintf("call-%d", index),
			Text:   "output",
		}})
	}

	tools := make([]string, 0, maxGuardianReviewItemsPerKind)
	for _, item := range state.items {
		if guardianReviewHistoryUserKind(item) {
			continue
		}
		tools = append(tools, item.ID)
	}
	// Half of the earlier entries plus the newly appended one remain.
	if want := maxGuardianReviewItemsPerKind/2 + 1; len(tools) != want {
		t.Fatalf("retained non-user items = %d, want %d", len(tools), want)
	}
	if want := fmt.Sprintf("tool-%d", maxGuardianReviewItemsPerKind/2); tools[0] != want {
		t.Fatalf("oldest retained tool = %q, want %q", tools[0], want)
	}
	if got := tools[len(tools)-1]; got != fmt.Sprintf("tool-%d", maxGuardianReviewItemsPerKind) {
		t.Fatalf("newest retained tool = %q", got)
	}
}

// TestGuardianReviewHistoryKeepsUserRetentionLimitLikeRust mirrors Rust #43798:
// user messages are not batched, so they keep the separate per-kind limit and
// lose one entry at a time.
func TestGuardianReviewHistoryKeepsUserRetentionLimitLikeRust(t *testing.T) {
	state := newGuardianReviewHistoryState([]session.Item{{
		ID: "restriction", Type: "message", Role: "user", Text: "Only publish privately.",
	}})
	for index := 0; index < maxGuardianReviewItemsPerKind; index++ {
		state.append([]session.Item{{
			ID:   fmt.Sprintf("user-%d", index),
			Type: "message", Role: "user", Text: "prompt",
		}})
	}

	users := make([]string, 0, maxGuardianReviewItemsPerKind)
	for _, item := range state.items {
		if guardianReviewHistoryUserKind(item) {
			users = append(users, item.ID)
		}
	}
	if len(users) != maxGuardianReviewItemsPerKind {
		t.Fatalf("retained user items = %d, want %d", len(users), maxGuardianReviewItemsPerKind)
	}
	if users[0] != "user-0" {
		t.Fatalf("oldest retained user = %q, want only the oldest message evicted", users[0])
	}
}
