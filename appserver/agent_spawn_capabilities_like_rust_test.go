package appserver

import (
	"context"
	"reflect"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
)

func spawnCapabilityTestRouter(t *testing.T) (*RuntimeRouter, *session.Store) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{
		ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: "/workspace", Model: "gpt-test", ModelProvider: "openai"},
	}
	if err := store.Create(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	return NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)}), store
}

func TestSpawnInheritsDisabledPluginsIndependentOfForkHistoryLikeRust(t *testing.T) {
	for _, forkTurns := range []string{"none", "all"} {
		t.Run(forkTurns, func(t *testing.T) {
			router, store := spawnCapabilityTestRouter(t)
			controller := newRuntimeAgentControllerForTurn(
				router, "parent", "parent-turn", "root-turn", "", "",
				"/workspace", 4, agent.VersionV2, nil,
			).(*runtimeAgentController)
			controller.disabledPluginIDs = []string{"calendar@market"}

			spawned, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{
				ForkTurns: forkTurnsStringPtr(forkTurns),
			})
			if err != nil {
				t.Fatalf("SpawnAgent(fork_turns=%s) error = %v", forkTurns, err)
			}
			record, err := store.Load(session.ThreadID(spawned.AgentID))
			if err != nil {
				t.Fatalf("load child: %v", err)
			}
			if got := stringSliceFromAny(record.Metadata.Extra["disabled_plugin_ids"]); !reflect.DeepEqual(got, []string{"calendar@market"}) {
				t.Fatalf("child disabled plugins = %#v", got)
			}
			if got := router.threadDisabledPluginIDs(spawned.AgentID); !reflect.DeepEqual(got, []string{"calendar@market"}) {
				t.Fatalf("thread disabled plugins = %#v", got)
			}

			// An explicit empty list overrides the parent's choices.
			empty := []string{}
			override, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{
				ForkTurns:         forkTurnsStringPtr(forkTurns),
				DisabledPluginIDs: &empty,
			})
			if err != nil {
				t.Fatalf("SpawnAgent(explicit empty) error = %v", err)
			}
			overrideRecord, err := store.Load(session.ThreadID(override.AgentID))
			if err != nil {
				t.Fatalf("load override child: %v", err)
			}
			if got, present := overrideRecord.Metadata.Extra["disabled_plugin_ids"]; !present || len(stringSliceFromAny(got)) != 0 {
				t.Fatalf("explicit empty child plugins = %#v (present=%v)", got, present)
			}
		})
	}
}

func TestSpawnInheritsCapabilityRootsIndependentOfForkHistoryLikeRust(t *testing.T) {
	root := SelectedCapabilityRoot{
		ID:       "deploy@market",
		Location: CapabilityRootLocation{Type: CapabilityRootLocationEnvironment, EnvironmentID: "local", Path: "/workspace/plugin"},
	}
	selections := []map[string]any{{
		"environmentId": "local",
		"cwd":           "/workspace",
		"config": map[string]any{
			"state": "ready",
			"config": map[string]any{
				"selectedCapabilityRoots": environmentConfigToAny(&EnvironmentConfig{
					SelectedCapabilityRoots: []SelectedCapabilityRoot{root},
				})["selectedCapabilityRoots"],
			},
		},
	}}
	for _, forkTurns := range []string{"none", "all"} {
		t.Run(forkTurns, func(t *testing.T) {
			router, store := spawnCapabilityTestRouter(t)
			controller := newRuntimeAgentControllerWithEnvironmentSelections(
				router, "parent", "/workspace", 4, agent.VersionV2, selections,
			)
			spawned, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{
				ForkTurns: forkTurnsStringPtr(forkTurns),
			})
			if err != nil {
				t.Fatalf("SpawnAgent(fork_turns=%s) error = %v", forkTurns, err)
			}
			record, err := store.Load(session.ThreadID(spawned.AgentID))
			if err != nil {
				t.Fatalf("load child: %v", err)
			}
			roots := router.inspectSelectedCapabilityRootsForThread(record).ReadyRoots
			if len(roots) != 1 || roots[0].ID != root.ID || roots[0].Location.Path != root.Location.Path {
				t.Fatalf("child capability roots = %#v, want %#v", roots, root)
			}
		})
	}
}
