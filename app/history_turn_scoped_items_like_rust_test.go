package app

import (
	"strings"
	"testing"

	"codex_go/session"
	"codex_go/tui/chatwidget"
)

// TestInteractiveHistoryScopesReusedItemIDsToOwningTurnsLikeRust covers Rust
// #61595: item IDs are unique only within a turn, so a page that reuses an ID
// across turns must group completions by (turnId, itemId). The middle turn is
// interrupted, so a misattributed boundary would both move the notice and emit
// a reversed range over the page.
func TestInteractiveHistoryScopesReusedItemIDsToOwningTurnsLikeRust(t *testing.T) {
	threadID := session.ThreadID("123e4567-e89b-12d3-a456-426614174301")
	items := []session.Item{
		interruptionHistoryItem(0, "prompt-1", "turn-first", "assistant", "first"),
		interruptionHistoryItem(1, "answer-0", "turn-first", "assistant", "shared-first"),
		interruptionHistoryItem(2, "middle", "turn-second", "assistant", "middle"),
		interruptionHistoryItem(3, "answer-0", "turn-last", "assistant", "shared-last"),
	}
	turns := []session.TurnSnapshot{
		{ID: "turn-first", Status: "interrupted"},
		{ID: "turn-second", Status: "completed"},
		{ID: "turn-last", Status: "completed"},
	}
	store := saveInterruptionHistoryRecord(t, threadID, turns, items)

	history := interactiveHistoryFor(store, threadID)
	groups := interactiveHistoryPageGroups(items, history.itemTurnIDs, history.turns)
	wantGroups := []interactiveHistoryPageGroup{{start: 0, end: 2, turn: 0}, {start: 2, end: 3, turn: 1}, {start: 3, end: 4, turn: 2}}
	if len(groups) != len(wantGroups) {
		t.Fatalf("groups = %#v, want %#v", groups, wantGroups)
	}
	for index := range wantGroups {
		if groups[index] != wantGroups[index] {
			t.Fatalf("groups = %#v, want %#v", groups, wantGroups)
		}
	}

	rows := interruptionHistoryTexts(interactivePageMessagesWithTurnBoundaries(store, threadID, items, ""))
	wantRows := []string{"first", "shared-first", chatwidget.ConversationInterruptedNotice, "middle", "shared-last"}
	if strings.Join(rows, "|") != strings.Join(wantRows, "|") {
		t.Fatalf("page rows = %q, want %q", rows, wantRows)
	}
}

// TestInteractiveHistorySkipsBackwardBoundariesLikeRust covers the second half
// of Rust #61595: a completion boundary that resolves before the range already
// emitted for this page is skipped instead of slicing the page backwards.
func TestInteractiveHistorySkipsBackwardBoundariesLikeRust(t *testing.T) {
	items := []session.Item{
		interruptionHistoryItem(0, "first", "turn-a", "assistant", "a"),
		interruptionHistoryItem(1, "second", "turn-b", "assistant", "b"),
		interruptionHistoryItem(2, "third", "turn-b", "assistant", "c"),
	}
	turns := []interactiveHistoryTurn{
		{id: "turn-a", status: "completed", firstItemID: "first", lastItemID: "first", itemCount: 1},
		{id: "turn-b", status: "completed", firstItemID: "second", lastItemID: "third", itemCount: 2},
		{id: "turn-c", status: "completed", firstItemID: "first", lastItemID: "first", itemCount: 1},
	}
	itemTurnIDs := map[uint64]string{1: "turn-a", 2: "turn-b", 3: "turn-b"}

	groups := interactiveHistoryPageGroups(items, itemTurnIDs, turns)
	want := []interactiveHistoryPageGroup{{start: 0, end: 1, turn: 0}, {start: 1, end: 3, turn: 1}}
	if len(groups) != len(want) {
		t.Fatalf("groups = %#v, want %#v", groups, want)
	}
	for index := range want {
		if groups[index] != want[index] {
			t.Fatalf("groups = %#v, want %#v", groups, want)
		}
	}
}
