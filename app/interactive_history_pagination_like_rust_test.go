package app

import (
	"fmt"
	"testing"
	"time"

	"codex_go/session"
	codextui "codex_go/tui"
)

// TestInteractiveHistoryPagesFromStoreLikeRust covers the embedded carrier for
// Rust app/history_pagination.rs (#46739): a resumed thread opens on its newest
// item page and the loader returns the pages before it, oldest first.
func TestInteractiveHistoryPagesFromStoreLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("123e4567-e89b-12d3-a456-426614174111")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	items := make([]session.Item, 0, 100)
	for index := 0; index < 100; index++ {
		role := "assistant"
		text := fmt.Sprintf("answer-%03d", index)
		if index%10 == 0 {
			role = "user"
			text = fmt.Sprintf("prompt-%03d", index)
		}
		items = append(items, session.Item{
			ID:        fmt.Sprintf("item-%03d", index),
			Type:      "message",
			Role:      role,
			Text:      text,
			CreatedAt: now.Add(time.Duration(index) * time.Second),
		})
	}
	if err := store.Save(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Title:     "Long thread",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Items:     items,
	}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	messages, cursor, hasOlder, err := interactiveSessionHistoryWindow(store, threadID, "")
	if err != nil {
		t.Fatalf("history window returned error: %v", err)
	}
	if !hasOlder || cursor == "" {
		t.Fatalf("window hasOlder=%v cursor=%q, want an older page", hasOlder, cursor)
	}
	if len(messages) != interactiveResumeInitialItemBudget {
		t.Fatalf("window messages = %d, want %d", len(messages), interactiveResumeInitialItemBudget)
	}
	if got := messages[0].Text; got != "prompt-040" {
		t.Fatalf("oldest window message = %q, want prompt-040", got)
	}
	if got := messages[len(messages)-1].Text; got != "answer-099" {
		t.Fatalf("newest window message = %q, want answer-099", got)
	}

	older, next, stillOlder, err := interactiveSessionHistoryPage(store, threadID, cursor, "")
	if err != nil {
		t.Fatalf("older page returned error: %v", err)
	}
	if stillOlder || next != "" {
		t.Fatalf("older page hasOlder=%v cursor=%q, want the beginning", stillOlder, next)
	}
	if len(older) != 40 {
		t.Fatalf("older page messages = %d, want 40", len(older))
	}
	if got := older[0].Text; got != "prompt-000" {
		t.Fatalf("oldest older message = %q, want prompt-000", got)
	}
	if got := older[len(older)-1].Text; got != "answer-039" {
		t.Fatalf("newest older message = %q, want answer-039", got)
	}
	if older[0].Role != codextui.RoleUser {
		t.Fatalf("oldest older role = %v, want a user prompt", older[0].Role)
	}
}
