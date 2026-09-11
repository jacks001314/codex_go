package turn

import (
	"strings"
	"testing"

	"codex_go/model"
)

func TestSeenIDsObserveIsFreshExactlyOnce(t *testing.T) {
	tracker := newSeenIDs()
	if !tracker.historyIndexed() {
		t.Fatal("new seen IDs should start with indexed history")
	}
	if !tracker.observeCallID("call-1") {
		t.Fatal("first observation of call-1 should be fresh")
	}
	if tracker.observeCallID("call-1") {
		t.Fatal("repeated observation of call-1 must not be fresh")
	}
	if !tracker.observeCallID("call-2") {
		t.Fatal("distinct call IDs stay fresh")
	}
}

func TestSeenIDsNamespacesAreIndependent(t *testing.T) {
	tracker := newSeenIDs()
	if !tracker.observeCallID("shared") {
		t.Fatal("call namespace first observation should be fresh")
	}
	if tracker.observeCallID("shared") {
		t.Fatal("repeated call ID must not be fresh")
	}
	// A runtime cell handle with the same spelling is a different namespace.
	if !tracker.observeRuntimeCellID("shared") {
		t.Fatal("runtime cell namespace is independent of call IDs")
	}
	if tracker.observeRuntimeCellID("shared") {
		t.Fatal("repeated runtime cell ID must not be fresh")
	}
}

func TestSeenIDsRejectsOversizedIDs(t *testing.T) {
	tracker := newSeenIDs()
	oversized := strings.Repeat("x", seenIDMaxIDBytes+1)
	if tracker.observeCallID(oversized) {
		t.Fatal("oversized ID cannot prove freshness")
	}
	// The bound is inclusive.
	atLimit := strings.Repeat("y", seenIDMaxIDBytes)
	if !tracker.observeCallID(atLimit) {
		t.Fatal("ID at the byte bound is accepted")
	}
}

func TestSeenIDsUnobservedHistoryIsNotIndexed(t *testing.T) {
	tracker := unobservedHistorySeenIDs()
	if tracker.historyIndexed() {
		t.Fatal("recording that started after initialization must not claim indexed history")
	}
}

func TestSeenIDsObserveHistoryIDsPreventsReuse(t *testing.T) {
	tracker := newSeenIDs()
	tracker.observeHistory([]any{
		map[string]any{"type": "function_call", "call_id": "historical-call"},
		map[string]any{"type": "function_call_output", "call_id": "historical-call"},
	})
	if !tracker.historyIndexed() {
		t.Fatal("small history fits the indexing budget")
	}
	if tracker.observeCallID("historical-call") {
		t.Fatal("an ID indexed from history must not be fresh")
	}
	if !tracker.observeCallID("new-call") {
		t.Fatal("an unseen ID stays fresh")
	}
}

func TestSeenIDsHistoryByteBudgetMarksUnindexed(t *testing.T) {
	tracker := newSeenIDs()
	// Exhaust the history ID-byte budget with bounded IDs spread across items.
	entries := seenIDMaxHistoryIDBytes/seenIDMaxIDBytes + 1
	items := make([]any, 0, entries)
	for index := 0; index < entries; index++ {
		items = append(items, map[string]any{
			"type":    "function_call",
			"call_id": strings.Repeat("q", seenIDMaxIDBytes),
		})
	}
	tracker.observeHistory(items)
	if tracker.historyIndexed() {
		t.Fatal("exhausting the history ID-byte budget must mark history unindexed")
	}
}

func TestHistoryObservedIDsExtractsCallAndCellIDs(t *testing.T) {
	agent := &model.AgentItem{Type: "function_call", CallID: "call-1"}
	agent.SetExecutedToolCallCell("cell-1")
	output := &ToolResponseItem{Type: "function_call_output", CallID: "call-1"}
	cases := []struct {
		name  string
		item  any
		wants []string
	}{
		{"agent item", agent, []string{"call-1", "cell-1"}},
		{"output item", output, []string{"call-1"}},
		{"map item", map[string]any{"type": "custom_tool_call", "call_id": "call-2",
			"internal_chat_message_metadata_passthrough": map[string]any{"cell_id": "cell-2"}},
			[]string{"call-2", "cell-2"}},
		{"non-call item", map[string]any{"type": "message", "role": "user"}, nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := historyObservedIDs(testCase.item)
			if len(got) != len(testCase.wants) {
				t.Fatalf("historyObservedIDs() = %#v, want %#v", got, testCase.wants)
			}
			for index := range got {
				if got[index] != testCase.wants[index] {
					t.Fatalf("historyObservedIDs() = %#v, want %#v", got, testCase.wants)
				}
			}
		})
	}
}
