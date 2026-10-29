package appserver

import (
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// TestThreadEnvironmentSelectionsUseTurnStartSnapshotLikeRust mirrors Rust
// #46557: the active turn reports the environment selections captured when it
// started (initial_environments), not selections saved while the turn runs.
func TestThreadEnvironmentSelectionsUseTurnStartSnapshotLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	record := &session.Record{ID: "thread-envs", SessionID: "thread-envs", CreatedAt: now, UpdatedAt: now, RecencyAt: now}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir()), ThreadRouter: NewRouter(store)})
	if err := router.persistThreadEnvironmentSelections("thread-envs", []map[string]any{{"environmentId": "env-saved"}}); err != nil {
		t.Fatalf("persistThreadEnvironmentSelections() error = %v", err)
	}

	params := &turn.TurnStartParams{
		ThreadID:     "thread-envs",
		Environments: []map[string]any{{"environmentId": "env-initial"}},
	}
	if err := router.registerActiveRuntimeTurn(params.ThreadID, "turn-1", nil, now.UnixMilli(), params); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}
	// A selection saved for the next turn must not rewrite the running turn.
	params.Environments = []map[string]any{{"environmentId": "env-next"}}

	selections := router.threadEnvironmentSelections("thread-envs")
	if len(selections) != 1 || selections[0]["environmentId"] != "env-initial" {
		t.Fatalf("active turn selections = %#v, want the turn-start snapshot env-initial", selections)
	}
	if _, ok := router.threads.ConsumeTurn("thread-envs", "turn-1", false); !ok {
		t.Fatal("turn-1 was not active")
	}
	selections = router.threadEnvironmentSelections("thread-envs")
	if len(selections) != 1 || selections[0]["environmentId"] != "env-saved" {
		t.Fatalf("selections after the turn ended = %#v, want the persisted env-saved", selections)
	}
}
