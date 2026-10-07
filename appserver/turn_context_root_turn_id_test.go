package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/rollout"
	"codex_go/session"
)

// Rust #51402 (`551bd409eb`): `TurnContextItem.root_turn_id` is written for
// every real turn and retained across recovery, so a turn whose `turn_started`
// record predates `turn_attribution` still recovers its root lineage from the
// model-context record.
//
// Rust references: `core/src/session/turn_context.rs::to_turn_context_item`
// (`root_turn_id: self.turn_metadata_state.root_turn_id()`),
// `core/src/state/session.rs::recovered_turn_start_options` (model-context root
// fallback), and `core/src/session/rollout_reconstruction_tests.rs`.
func TestTurnContextRootTurnIDPersistedLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "gpt-5", ContextWindow: 200000, CompHash: "hash-current"},
		}})),
	})
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: home}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read started thread error: %v", err)
	}
	router.recordRuntimeTurnContext(threadID, "turn-1", "root-a", &appTurnRunConfig{Model: "gpt-5"}, record)

	rolloutPath := router.services.ThreadRouter.threadRolloutPath(record)
	raw, err := os.ReadFile(rolloutPath)
	if err != nil {
		t.Fatalf("ReadFile(rollout) error = %v", err)
	}
	if !strings.Contains(string(raw), `"turn_context"`) || !strings.Contains(string(raw), `"root_turn_id":"root-a"`) {
		t.Fatalf("Go-written turn_context must persist root_turn_id:\n%s", raw)
	}

	// The legacy fallback reader is reachable with Go-produced data: no
	// `turn_attribution` was persisted, so recovery falls back to the
	// model-context root.
	cold, err := rollout.RecordFromPath(rolloutPath, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if opts := cold.RecoveredTurnStartOptions("turn-1"); opts.RootTurnID != "root-a" {
		t.Fatalf("recovered root = %q, want root-a", opts.RootTurnID)
	}
	// A turn the record does not name recovers nothing.
	if other := cold.RecoveredTurnStartOptions("turn-2"); other.RootTurnID != "" {
		t.Fatalf("unrelated turn recovered root %q, want empty", other.RootTurnID)
	}
}
