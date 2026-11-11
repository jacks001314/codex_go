package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/session"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	"codex_go/utils"
)

// saveInterruptionHistoryRecord stores one thread whose rollout turns pin the
// item attribution the pagination loader reads back.
func saveInterruptionHistoryRecord(t *testing.T, threadID session.ThreadID, turns []session.TurnSnapshot, items []session.Item) *session.Store {
	t.Helper()
	store := session.NewStore(t.TempDir())
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	if err := store.Save(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Title:     "Interrupted history",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{RolloutTurns: turns},
		Items:     items,
	}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	return store
}

func interruptionHistoryItem(index int, id string, turnID string, role string, text string) session.Item {
	return session.Item{
		ID:        id,
		Type:      "message",
		Role:      role,
		Text:      text,
		CreatedAt: time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC).Add(time.Duration(index) * time.Second),
		Metadata:  map[string]any{"turnId": turnID},
	}
}

func interruptionHistoryTexts(messages []codextui.Message) []string {
	texts := make([]string, 0, len(messages))
	for _, message := range messages {
		texts = append(texts, utils.StripANSI(message.Text))
	}
	return texts
}

// TestInteractiveOlderHistoryRestoresInterruptionAtTurnBoundaryLikeRust covers
// Rust #60554: the page that holds an interrupted turn's last item restores the
// neutral notice after that item, while the page holding the rest of the turn
// stays notice-free.
func TestInteractiveOlderHistoryRestoresInterruptionAtTurnBoundaryLikeRust(t *testing.T) {
	threadID := session.ThreadID("123e4567-e89b-12d3-a456-426614174201")
	items := []session.Item{
		interruptionHistoryItem(0, "item-canceled", "turn-canceled", "assistant", "Searched the web for canceled"),
		interruptionHistoryItem(1, "item-older", "turn-audit", "assistant", "Searched the web for older"),
		interruptionHistoryItem(2, "item-newer", "turn-audit", "assistant", "Searched the web for newer"),
	}
	turns := []session.TurnSnapshot{
		{ID: "turn-canceled", Status: "interrupted"},
		{ID: "turn-audit", Status: "completed"},
	}
	store := saveInterruptionHistoryRecord(t, threadID, turns, items)

	messages := interactivePageMessagesWithTurnBoundaries(store, threadID, items[:2], "")
	got := interruptionHistoryTexts(messages)
	want := []string{"Searched the web for canceled", chatwidget.ConversationInterruptedNotice, "Searched the web for older"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("page rows = %q, want %q", got, want)
	}

	// The audit turn ends on the next page, so this page keeps no extra row.
	rest := interruptionHistoryTexts(interactivePageMessagesWithTurnBoundaries(store, threadID, items[1:], ""))
	if strings.Join(rest, "|") != "Searched the web for older|Searched the web for newer" {
		t.Fatalf("second page rows = %q", rest)
	}
}

// TestInteractiveHistoryPageWiresTurnBoundariesLikeRust proves the store
// loader itself applies the boundary rule: the cursor page that ends the
// interrupted turn restores the notice, and the newest window stays plain.
func TestInteractiveHistoryPageWiresTurnBoundariesLikeRust(t *testing.T) {
	threadID := session.ThreadID("123e4567-e89b-12d3-a456-426614174204")
	items := make([]session.Item, 0, 120)
	for index := 0; index < 120; index++ {
		turnID := "turn-done"
		switch {
		case index >= 59:
			turnID = "turn-later"
		case index >= 30:
			turnID = "turn-canceled"
		}
		items = append(items, interruptionHistoryItem(index, fmt.Sprintf("item-%03d", index), turnID, "assistant", fmt.Sprintf("row-%03d", index)))
	}
	turns := []session.TurnSnapshot{
		{ID: "turn-done", Status: "completed"},
		{ID: "turn-canceled", Status: "interrupted"},
		{ID: "turn-later", Status: "completed"},
	}
	store := saveInterruptionHistoryRecord(t, threadID, turns, items)

	window, cursor, hasOlder, err := interactiveSessionHistoryWindow(store, threadID, "")
	if err != nil || !hasOlder || cursor == "" {
		t.Fatalf("window err=%v hasOlder=%v cursor=%q", err, hasOlder, cursor)
	}
	for _, message := range window {
		if strings.Contains(utils.StripANSI(message.Text), chatwidget.ConversationInterruptedNotice) {
			t.Fatal("the initial window restored an older-page interruption row")
		}
	}
	older, _, _, err := interactiveSessionHistoryPage(store, threadID, cursor, "")
	if err != nil {
		t.Fatalf("older page returned error: %v", err)
	}
	texts := interruptionHistoryTexts(older)
	if len(texts) != 61 {
		t.Fatalf("older page rows = %d, want 60 items plus the notice", len(texts))
	}
	if texts[59] != chatwidget.ConversationInterruptedNotice || texts[58] != "row-058" {
		t.Fatalf("notice placement = %q between %q and %q", texts[59], texts[58], texts[60])
	}
}

// TestRemoteOlderHistoryRestoresInterruptionBoundaryLikeRust covers the
// daemon-owned loader, where each turn page owns its own boundary.
func TestRemoteOlderHistoryRestoresInterruptionBoundaryLikeRust(t *testing.T) {
	turns := []appserver.Turn{
		{ID: "turn-done", Status: appserver.TurnStatusCompleted, Items: []appserver.ThreadItem{{ID: "item-1", Type: "agentMessage", Role: "assistant", Text: "finished work"}}},
		{ID: "turn-canceled", Status: appserver.TurnStatusInterrupted, Items: []appserver.ThreadItem{{ID: "item-2", Type: "agentMessage", Role: "assistant", Text: "partial answer"}}},
	}
	got := interruptionHistoryTexts(remoteTUIThreadMessagesFromOlderTurns(turns, reasoningProjectionChatWidget, false))
	want := []string{"finished work", "partial answer", chatwidget.ConversationInterruptedNotice}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("remote page rows = %q, want %q", got, want)
	}
}

// TestInteractiveOlderHistoryInterruptionWithoutItemsPrecedesHistoryLikeRust
// covers Rust's itemless-interrupted branch: a turn cancelled before any item
// published keeps the preceding boundary, which here is the start of history.
func TestInteractiveOlderHistoryInterruptionWithoutItemsPrecedesHistoryLikeRust(t *testing.T) {
	threadID := session.ThreadID("123e4567-e89b-12d3-a456-426614174202")
	items := []session.Item{
		interruptionHistoryItem(0, "item-live", "turn-live", "assistant", "kept working"),
	}
	turns := []session.TurnSnapshot{
		{ID: "turn-canceled", Status: "interrupted"},
		{ID: "turn-live", Status: "completed"},
	}
	store := saveInterruptionHistoryRecord(t, threadID, turns, items)

	got := interruptionHistoryTexts(interactivePageMessagesWithTurnBoundaries(store, threadID, items, ""))
	want := []string{chatwidget.ConversationInterruptedNotice, "kept working"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("page rows = %q, want %q", got, want)
	}
}

// TestInteractiveOlderHistoryKeepsSplitTurnsOnOnePageLikeRust covers a turn
// whose items straddle two pages: only the page with its last item gains the
// interruption notice.
func TestInteractiveOlderHistoryKeepsSplitTurnsOnOnePageLikeRust(t *testing.T) {
	threadID := session.ThreadID("123e4567-e89b-12d3-a456-426614174203")
	items := make([]session.Item, 0, 100)
	for index := 0; index < 100; index++ {
		turnID := "turn-done"
		if index >= 50 {
			turnID = "turn-canceled"
		}
		items = append(items, interruptionHistoryItem(index, fmt.Sprintf("item-%03d", index), turnID, "assistant", fmt.Sprintf("row-%03d", index)))
	}
	turns := []session.TurnSnapshot{
		{ID: "turn-done", Status: "completed"},
		{ID: "turn-canceled", Status: "interrupted"},
	}
	store := saveInterruptionHistoryRecord(t, threadID, turns, items)

	first := interruptionHistoryTexts(interactivePageMessagesWithTurnBoundaries(store, threadID, items[:40], ""))
	if len(first) != 40 {
		t.Fatalf("page ending inside the completed turn rendered %d rows, want 40", len(first))
	}
	if strings.Contains(strings.Join(first, "|"), chatwidget.ConversationInterruptedNotice) {
		t.Fatalf("page without the interrupted turn's last item showed its notice")
	}
	second := interruptionHistoryTexts(interactivePageMessagesWithTurnBoundaries(store, threadID, items[40:], ""))
	if len(second) != 61 {
		t.Fatalf("page with the interrupted turn end rendered %d rows, want 61", len(second))
	}
	if second[len(second)-1] != chatwidget.ConversationInterruptedNotice {
		t.Fatalf("last row = %q, want the interruption notice", second[len(second)-1])
	}
}
