package appserver

import (
	"os"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

func createContextLimitTestThread(t *testing.T, store *session.Store, id string, parent string) {
	t.Helper()
	now := time.Now().UTC()
	metadata := session.Metadata{HistoryMode: string(ThreadHistoryLegacy)}
	if parent != "" {
		metadata.Originator = "subagent"
		metadata.ThreadSource = "subAgentThreadSpawn"
	}
	if err := store.Create(&session.Record{
		ID: session.ThreadID(id), SessionID: id, ParentThreadID: session.ThreadID(parent),
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: metadata,
	}); err != nil {
		t.Fatalf("Create(%s) error = %v", id, err)
	}
}

// Rust #52659: with the feature enabled a subagent turn drops inherited
// context-window and auto-compaction overrides so the model defaults apply,
// while parent threads keep them.
func TestSubagentDefaultContextLimitsFromTurnConfigLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	createContextLimitTestThread(t, store, "parent", "")
	createContextLimitTestThread(t, store, "child", "parent")
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })

	child := &turn.TurnStartParams{ThreadID: "child", Config: map[string]any{
		"features":                       map[string]any{"subagent_default_context_limits": true},
		"model_context_window":           int64(111),
		"model_auto_compact_token_limit": int64(222),
	}}
	if err := router.prepareTurnStartParams(child); err != nil {
		t.Fatalf("prepareTurnStartParams(child) error = %v", err)
	}
	if child.Config["model_context_window"] != nil || child.Config["model_auto_compact_token_limit"] != nil {
		t.Fatalf("subagent overrides = %#v, want cleared values", child.Config)
	}

	parent := &turn.TurnStartParams{ThreadID: "parent", Config: map[string]any{
		"features":                       map[string]any{"subagent_default_context_limits": true},
		"model_context_window":           int64(111),
		"model_auto_compact_token_limit": int64(222),
	}}
	if err := router.prepareTurnStartParams(parent); err != nil {
		t.Fatalf("prepareTurnStartParams(parent) error = %v", err)
	}
	if parent.Config["model_context_window"] != int64(111) || parent.Config["model_auto_compact_token_limit"] != int64(222) {
		t.Fatalf("parent overrides = %#v, want preserved values", parent.Config)
	}
}

// The feature also clears a global configuration override for subagents.
func TestSubagentDefaultContextLimitsFromGlobalConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(
		"model_context_window = 100000\nmodel_auto_compact_token_limit = 90000\n[features]\nsubagent_default_context_limits = true\n",
	), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	store := session.NewStore(t.TempDir())
	createContextLimitTestThread(t, store, "parent-global", "")
	createContextLimitTestThread(t, store, "child-global", "parent-global")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	t.Cleanup(func() { _ = router.Close() })

	child := &turn.TurnStartParams{ThreadID: "child-global"}
	if err := router.prepareTurnStartParams(child); err != nil {
		t.Fatalf("prepareTurnStartParams(child) error = %v", err)
	}
	if child.Config["model_context_window"] != nil || child.Config["model_auto_compact_token_limit"] != nil {
		t.Fatalf("subagent overrides = %#v, want the global values cleared", child.Config)
	}
	parent := &turn.TurnStartParams{ThreadID: "parent-global"}
	if err := router.prepareTurnStartParams(parent); err != nil {
		t.Fatalf("prepareTurnStartParams(parent) error = %v", err)
	}
	if parent.Config["model_context_window"] != nil || parent.Config["model_auto_compact_token_limit"] != nil {
		t.Fatalf("parent config = %#v, want no injected overrides", parent.Config)
	}
}
