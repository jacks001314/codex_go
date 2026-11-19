package app

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/session"
	codextui "codex_go/tui"
)

// TestInteractiveResumeHandlerPagesLongThreadLikeRust covers the bounded read
// the handler now uses: a long thread resumes on its newest page with the same
// transcript and cursor the store-backed page returns.
func TestInteractiveResumeHandlerPagesLongThreadLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	store := session.NewStore(filepath.Join(auth.DefaultCodexHome(), "sessions"))
	threadID := session.ThreadID("123e4567-e89b-12d3-a456-426614174444")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cwd := `D:\repo`
	items := make([]session.Item, 0, 130)
	for index := 0; index < 130; index++ {
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
		Title:     "Long resume thread",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: cwd, Source: "cli", ModelProvider: "openai", HistoryMode: "legacy"},
		Items:     items,
	}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	response, err := interactiveResumeSessionHandler(nil)(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{BackendID: codextui.ProcessBackendID(), ThreadID: string(threadID)},
	})
	if err != nil {
		t.Fatalf("resume handler error = %v", err)
	}
	wantMessages, wantCursor, wantOlder, err := interactiveSessionHistoryWindow(store, threadID, cwd)
	if err != nil {
		t.Fatalf("store history window returned error: %v", err)
	}
	if !reflect.DeepEqual(response.Messages, wantMessages) {
		t.Fatalf("resume messages = %#v, want %#v", response.Messages, wantMessages)
	}
	if response.OlderHistoryCursor != wantCursor || response.HasOlderHistory != wantOlder {
		t.Fatalf("resume cursor=%q older=%v, want cursor=%q older=%v", response.OlderHistoryCursor, response.HasOlderHistory, wantCursor, wantOlder)
	}
	if len(response.Messages) != interactiveResumeInitialItemBudget {
		t.Fatalf("resume messages = %d, want %d", len(response.Messages), interactiveResumeInitialItemBudget)
	}
}
