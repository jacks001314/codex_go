package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// priorityCatalogModel returns a bundled model that advertises the `priority`
// service tier, so a spawn override can be validated like Rust #63376.
func priorityCatalogModel(t *testing.T) model.ModelPreset {
	t.Helper()
	for _, preset := range model.NewBundledModelsManager().ListModels(model.RefreshInMemory) {
		for _, tier := range preset.ServiceTiers {
			if tier == "priority" {
				return preset
			}
		}
	}
	t.Skip("the bundled catalog advertises no priority service tier")
	return model.ModelPreset{}
}

// Mirrors Rust #63376 `subagent_service_tier`: a spawn may select a tier for one
// child, the choice is persisted on the child, its own turns keep it, and
// grandchildren fall back to the ordinary (root) tier.
func TestSubagentSpawnServiceTierLikeRust(t *testing.T) {
	preset := priorityCatalogModel(t)
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: preset.Model, ModelProvider: "openai", ServiceTier: "flex"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	exposing := map[string]any{
		"features": map[string]any{
			"fast_mode": true,
			"multi_agent_v2": map[string]any{
				"expose_spawn_agent_service_tier": true,
			},
		},
	}
	if err := router.threads.RegisterTurn("parent", "parent-turn", func() {}, now.UnixMilli(), &turn.TurnStartParams{
		ThreadID: "parent",
		Config:   exposing,
	}); err != nil {
		t.Fatal(err)
	}
	controller := newRuntimeAgentControllerForTurn(router, "parent", "parent-turn", "root-turn", "", "", parent.Metadata.CWD, 4, agent.VersionV2, nil).(*runtimeAgentController)

	message := "child work"
	tier := "priority"
	child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{
		TaskName:    "worker",
		Message:     &message,
		Model:       &preset.Model,
		ServiceTier: &tier,
	})
	if err != nil {
		t.Fatalf("SpawnAgent() error = %v", err)
	}
	// The spawned child keeps the store directory open, which makes TempDir
	// cleanup fail on Windows; shut it down and wait for its turn worker to
	// stop writing before the test returns.
	t.Cleanup(func() {
		_, _ = controller.CloseAgent(context.Background(), &agent.CloseAgentArgs{Target: child.AgentID})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := router.threads.WaitForTurnWorkers(ctx); err != nil {
			t.Errorf("wait for the child turn workers: %v", err)
		}
	})
	childRecord, err := store.Read(session.ThreadID(child.AgentID), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if childRecord.Metadata.ServiceTier != "priority" {
		t.Fatalf("child service tier = %q, want priority", childRecord.Metadata.ServiceTier)
	}
	if got, _ := childRecord.Metadata.Extra[spawnServiceTierOverrideExtraKey].(string); got != "priority" {
		t.Fatalf("child override snapshot = %q, want priority", got)
	}
	if got, _ := childRecord.Metadata.Extra[spawnServiceTierInheritedExtraKey].(string); got != "flex" {
		t.Fatalf("child inherited snapshot = %q, want flex", got)
	}
	active := router.threads.ActiveTurn(child.AgentID)
	if active == nil || active.Params == nil || active.Params.ServiceTier == nil || *active.Params.ServiceTier != "priority" {
		t.Fatalf("child turn params = %#v, want a priority tier", active)
	}
	// Every turn of the child keeps the override instead of following the root.
	if got := router.subagentRootServiceTier(child.AgentID); got != "priority" {
		t.Fatalf("subagentRootServiceTier(child) = %q, want priority", got)
	}

	// The child's own spawns use the ordinary root tier, never the override.
	childController := newRuntimeAgentControllerForTurn(router, child.AgentID, "", "", "", "", childRecord.Metadata.CWD, 4, agent.VersionV2, nil).(*runtimeAgentController)
	grandchildMessage := "grandchild work"
	grandchild, err := childController.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "nested", Message: &grandchildMessage})
	if err != nil {
		t.Fatalf("grandchild SpawnAgent() error = %v", err)
	}
	grandchildActive := router.threads.ActiveTurn(grandchild.AgentID)
	if grandchildActive == nil || grandchildActive.Params == nil || grandchildActive.Params.ServiceTier == nil || *grandchildActive.Params.ServiceTier != "flex" {
		t.Fatalf("grandchild turn params = %#v, want the inherited flex tier", grandchildActive)
	}
}

// Mirrors Rust #63376: an explicit tier the child model does not advertise is a
// spawn error, and the argument is ignored while the exposing setting is off.
func TestSubagentSpawnServiceTierValidationLikeRust(t *testing.T) {
	preset := priorityCatalogModel(t)
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: preset.Model, ModelProvider: "openai"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	if err := router.threads.RegisterTurn("parent", "parent-turn", func() {}, now.UnixMilli(), &turn.TurnStartParams{
		ThreadID: "parent",
		Config: map[string]any{
			"features": map[string]any{
				"fast_mode": true,
				"multi_agent_v2": map[string]any{
					"expose_spawn_agent_service_tier": true,
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	controller := newRuntimeAgentControllerForTurn(router, "parent", "parent-turn", "root-turn", "", "", parent.Metadata.CWD, 4, agent.VersionV2, nil).(*runtimeAgentController)
	message := "child work"
	unsupported := "unadvertised-tier"
	_, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{
		TaskName:    "worker",
		Message:     &message,
		Model:       &preset.Model,
		ServiceTier: &unsupported,
	})
	if err == nil || !strings.Contains(err.Error(), "Service tier `unadvertised-tier` is unavailable for model `"+preset.Model+"`") {
		t.Fatalf("unsupported tier error = %v", err)
	}

	// `default` is always accepted.
	tier := "default"
	defaultChild, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{
		TaskName:    "worker",
		Message:     &message,
		Model:       &preset.Model,
		ServiceTier: &tier,
	})
	if err != nil {
		t.Fatalf("default tier rejected: %v", err)
	}
	defaultRecord, err := store.Read(session.ThreadID(defaultChild.AgentID), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if defaultRecord.Metadata.ServiceTier != "default" {
		t.Fatalf("default tier snapshot = %q", defaultRecord.Metadata.ServiceTier)
	}

	// Without the exposing setting the argument is dropped.
	offRouter := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	if err := offRouter.threads.RegisterTurn("parent", "parent-turn", func() {}, now.UnixMilli(), &turn.TurnStartParams{ThreadID: "parent"}); err != nil {
		t.Fatal(err)
	}
	offController := newRuntimeAgentControllerForTurn(offRouter, "parent", "parent-turn", "root-turn", "", "", parent.Metadata.CWD, 4, agent.VersionV2, nil).(*runtimeAgentController)
	offChild, err := offController.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{
		TaskName:    "gated",
		Message:     &message,
		Model:       &preset.Model,
		ServiceTier: &unsupported,
	})
	if err != nil {
		t.Fatalf("gated spawn error = %v", err)
	}
	offRecord, err := store.Read(session.ThreadID(offChild.AgentID), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if offRecord.Metadata.ServiceTier != "" {
		t.Fatalf("gated spawn stored tier = %q, want none", offRecord.Metadata.ServiceTier)
	}
	// Both spawned children keep writing into the store directory; stop them and
	// let their turn workers finish before the temp dirs are removed.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = controller.CloseAgent(ctx, &agent.CloseAgentArgs{Target: defaultChild.AgentID})
		_, _ = offController.CloseAgent(ctx, &agent.CloseAgentArgs{Target: offChild.AgentID})
		if err := router.threads.WaitForTurnWorkers(ctx); err != nil {
			t.Errorf("wait for the child turn workers: %v", err)
		}
		if err := offRouter.threads.WaitForTurnWorkers(ctx); err != nil {
			t.Errorf("wait for the gated child turn workers: %v", err)
		}
	})
}
