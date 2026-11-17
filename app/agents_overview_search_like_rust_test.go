package app

import (
	"testing"
	"time"

	"codex_go/session"
)

// Rust #61926: history search also matches case-insensitive substrings of
// thread IDs and Git branch names.
func TestEmbeddedAgentsOverviewSearchMatchesThreadIDAndBranchLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	threadID := session.ThreadID("abcdef01-0000-4000-8000-000000000042")
	if err := store.Save(&session.Record{
		ID:        threadID,
		Title:     "Task to find",
		Preview:   "preview",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           "/work/repo",
			ModelProvider: "openai",
			Source:        "cli",
			Git:           map[string]string{"branch": "Fix/Resume"},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	search := embeddedAgentsOverviewSearch(store)
	for _, query := range []string{"FIX/RES", "ABCDEF", "TASK TO FIND"} {
		rows, err := search(query)
		if err != nil {
			t.Fatalf("search %q error = %v", query, err)
		}
		if len(rows) != 1 || rows[0].ThreadID != string(threadID) {
			t.Fatalf("search %q rows = %#v", query, rows)
		}
	}
	rows, err := search("no-such-task")
	if err != nil || len(rows) != 0 {
		t.Fatalf("non-matching search = %#v, %v", rows, err)
	}
}
