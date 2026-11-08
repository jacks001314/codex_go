package appserver

import (
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/model"
	"codex_go/session"
)

// catalogStrategySplitManager serves a different preset list for the in-memory
// catalog than for the on-disk one, standing in for a parent whose loaded
// snapshot is newer than the cache on disk (Rust #60049).
type catalogStrategySplitManager struct {
	model.ModelsManager
	memory  []model.ModelPreset
	offline []model.ModelPreset
}

func (m catalogStrategySplitManager) ListModels(strategy model.RefreshStrategy) []model.ModelPreset {
	if strategy == model.RefreshInMemory {
		return m.memory
	}
	return m.offline
}

// TestChildAgentInheritsParentCatalogLikeRust pins Rust #60049: a child agent
// resolves its startup model against the catalog its selector already loaded.
// Reading the on-disk cache instead would let an older catalog replace the
// parent's snapshot and drop capabilities such as tool_search.
func TestChildAgentInheritsParentCatalogLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "parent-model", ModelProvider: "openai"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	split := catalogStrategySplitManager{
		ModelsManager: model.NewStaticModelsManager(model.BundledModelsResponse()),
		memory:        []model.ModelPreset{{Model: "parent-snapshot-model", Visibility: model.VisibilityList}},
		offline:       []model.ModelPreset{{Model: "stale-cache-model", Visibility: model.VisibilityList}},
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Agent:        &model.ResponsesAgentRunner{ModelsManager: split},
	})
	controller := newRuntimeAgentControllerForTurn(router, "parent", "parent-turn", "root-turn", "", "", parent.Metadata.CWD, 4, agent.VersionV2, nil).(*runtimeAgentController)

	args := &agent.SpawnAgentArgs{Model: stringPtr("parent-snapshot-model")}
	if err := controller.resolveSpawnModelOverrides(args); err != nil {
		t.Fatalf("child did not resolve the parent's loaded catalog: %v", err)
	}
	if args.Model == nil || *args.Model != "parent-snapshot-model" {
		t.Fatalf("resolved child model = %#v, want the parent snapshot model", args.Model)
	}

	stale := &agent.SpawnAgentArgs{Model: stringPtr("stale-cache-model")}
	if err := controller.resolveSpawnModelOverrides(stale); err == nil {
		t.Fatal("child resolved a model that only exists in the older on-disk catalog")
	}
}
