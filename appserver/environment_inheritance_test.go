package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #49075 `TurnEnvironmentSnapshot::inheritable_selections`: a spawned
// child keeps the ready and still-starting attachments of the spawning step and
// drops a failed one instead of inheriting it.
func TestInheritableEnvironmentSelectionsLikeRust(t *testing.T) {
	ready := map[string]any{
		"environmentId": "remote",
		"cwd":           "/remote",
		"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigReady, Config: &EnvironmentConfig{}}),
	}
	pending := map[string]any{
		"environmentId": "starting",
		"cwd":           "/starting",
		"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigPending}),
	}
	failed := map[string]any{
		"environmentId": "broken",
		"cwd":           "/broken",
		"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigFailed, Error: "owner could not prepare it"}),
	}
	threadDerived := map[string]any{"environmentId": "local", "cwd": "/local"}

	cases := []struct {
		name string
		in   []map[string]any
		want []string
	}{
		{name: "keeps ready pending and thread-derived", in: []map[string]any{ready, pending, threadDerived, failed}, want: []string{"remote", "starting", "local"}},
		{name: "drops the failed attachment only", in: []map[string]any{failed}, want: nil},
		{name: "nil stays nil", in: nil, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inheritableEnvironmentSelections(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("inheritableEnvironmentSelections() = %#v, want ids %v", got, tc.want)
			}
			for i, id := range tc.want {
				if selectionEnvironmentID(got[i]) != id {
					t.Fatalf("selection %d = %#v, want environment %q", i, got[i], id)
				}
			}
		})
	}
	// The helper must not alias its input.
	got := inheritableEnvironmentSelections([]map[string]any{ready})
	got[0]["cwd"] = "/mutated"
	if ready["cwd"] != "/remote" {
		t.Fatalf("inheritableEnvironmentSelections aliased its input: %#v", ready)
	}
}

// A spawned child (and grandchild) inherits the spawning step's ready and
// pending attachments, so the starting environment is not dropped at the spawn
// boundary (Rust #49075).
func TestRuntimeAgentControllerChildInheritsInheritableEnvironmentSelectionsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: "/primary"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	graph := agent.NewMemoryStore()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), SpawnGraph: graph})
	selections := []map[string]any{
		{"environmentId": "remote-primary", "cwd": "/primary"},
		{
			"environmentId": "remote-starting",
			"cwd":           "/starting",
			"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigPending}),
		},
		{
			"environmentId": "remote-broken",
			"cwd":           "/broken",
			"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigFailed, Error: "owner gave up"}),
		},
	}
	controller := newRuntimeAgentControllerWithEnvironmentSelections(router, "parent", parent.Metadata.CWD, 4, agent.VersionV2, selections)
	child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{})
	if err != nil {
		t.Fatal(err)
	}
	childRecord, err := store.Load(session.ThreadID(child.AgentID))
	if err != nil {
		t.Fatal(err)
	}
	childSelections := environmentSelectionsFromAny(childRecord.Metadata.Extra[runtimeEnvironmentSelectionsExtraKey])
	if len(childSelections) != 2 || selectionEnvironmentID(childSelections[0]) != "remote-primary" || selectionEnvironmentID(childSelections[1]) != "remote-starting" {
		t.Fatalf("child selections = %#v, want the ready and pending attachments only", childSelections)
	}

	childController := newRuntimeAgentControllerWithEnvironmentSelections(router, child.AgentID, childRecord.Metadata.CWD, 4, agent.VersionV2, childSelections)
	grandchild, err := childController.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{})
	if err != nil {
		t.Fatal(err)
	}
	grandchildRecord, err := store.Load(session.ThreadID(grandchild.AgentID))
	if err != nil {
		t.Fatal(err)
	}
	grandchildSelections := environmentSelectionsFromAny(grandchildRecord.Metadata.Extra[runtimeEnvironmentSelectionsExtraKey])
	if len(grandchildSelections) != 2 || selectionEnvironmentID(grandchildSelections[1]) != "remote-starting" {
		t.Fatalf("grandchild selections = %#v, want the inherited pending attachment", grandchildSelections)
	}
	state, err := environmentConfigStateFromAnyMap(grandchildSelections[1])
	if err != nil || state.Kind != EnvironmentConfigPending {
		t.Fatalf("grandchild pending state = %#v, %v", state, err)
	}
}

// Rust #49075 `follow_inherited_environment_configurations`: when the owner's
// pending attachment resolves, the first result follows the descendants that
// inherited that pending attachment, through the production turn/start path
// that publishes a thread's selections.
func TestRuntimeRouterPropagatesOwnerEnvironmentResultToDescendantsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	graph := agent.NewMemoryStore()
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "sh"}, "")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "remote", ExecServerURL: "ws://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("ok"),
		ThreadStatus: NewThreadStatusManager(),
		Environment:  manager,
		SpawnGraph:   graph,
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	now := time.Now().UTC()
	pending := map[string]any{
		"environmentId": "remote",
		"cwd":           "/remote",
		"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigPending}),
	}
	create := func(id, parent string) {
		record := &session.Record{
			ID: session.ThreadID(id), SessionID: id, ParentThreadID: session.ThreadID(parent),
			CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{CWD: "/remote", Extra: map[string]any{runtimeEnvironmentSelectionsExtraKey: []map[string]any{cloneAnyMap(pending)}}},
		}
		if err := store.Create(record); err != nil {
			t.Fatal(err)
		}
	}
	create("root", "")
	create("child", "root")
	create("grandchild", "child")
	if err := graph.UpsertThreadSpawnEdge("root", "child", agent.ThreadSpawnEdgeOpen); err != nil {
		t.Fatal(err)
	}
	if err := graph.UpsertThreadSpawnEdge("child", "grandchild", agent.ThreadSpawnEdgeOpen); err != nil {
		t.Fatal(err)
	}

	// The owner resolves the attachment on its own next turn.
	resolved := []map[string]any{{
		"environmentId": "remote",
		"cwd":           "/remote",
		"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigReady, Config: &EnvironmentConfig{AllowLoginShell: true}}),
	}}
	response := router.Handle(requestWithParams(t, IntID(1), MethodTurnStart, turn.TurnStartParams{ThreadID: "root", Environments: resolved}))
	if response.Error != nil {
		t.Fatalf("turn/start error = %+v", response.Error)
	}

	want := environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigReady, Config: &EnvironmentConfig{AllowLoginShell: true}})
	for _, id := range []string{"child", "grandchild"} {
		record, err := store.Load(session.ThreadID(id))
		if err != nil {
			t.Fatal(err)
		}
		selections := environmentSelectionsFromAny(record.Metadata.Extra[runtimeEnvironmentSelectionsExtraKey])
		if len(selections) != 1 {
			t.Fatalf("%s selections = %#v", id, selections)
		}
		state, err := environmentConfigStateFromAnyMap(selections[0])
		if err != nil {
			t.Fatal(err)
		}
		if state.Kind != EnvironmentConfigReady || state.Config == nil || !state.Config.AllowLoginShell {
			t.Fatalf("%s inherited state = %#v, want the owner's ready result %#v", id, selections[0]["config"], want)
		}
	}
}

// A root failure reaches child and grandchild (Rust #49075
// `pending_environment_failure_reaches_child_and_grandchild`).
func TestRuntimeRouterPropagatesOwnerEnvironmentFailureToDescendantsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	graph := agent.NewMemoryStore()
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "sh"}, "")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "remote", ExecServerURL: "ws://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("ok"),
		ThreadStatus: NewThreadStatusManager(),
		Environment:  manager,
		SpawnGraph:   graph,
	})
	router.SetNotificationSink(NewNotificationBuffer())

	now := time.Now().UTC()
	pending := map[string]any{
		"environmentId": "remote",
		"cwd":           "/remote",
		"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigPending}),
	}
	create := func(id, parent string) {
		record := &session.Record{
			ID: session.ThreadID(id), SessionID: id, ParentThreadID: session.ThreadID(parent),
			CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{CWD: "/remote", Extra: map[string]any{runtimeEnvironmentSelectionsExtraKey: []map[string]any{cloneAnyMap(pending)}}},
		}
		if err := store.Create(record); err != nil {
			t.Fatal(err)
		}
	}
	create("root", "")
	create("child", "root")
	create("grandchild", "child")
	if err := graph.UpsertThreadSpawnEdge("root", "child", agent.ThreadSpawnEdgeOpen); err != nil {
		t.Fatal(err)
	}
	if err := graph.UpsertThreadSpawnEdge("child", "grandchild", agent.ThreadSpawnEdgeOpen); err != nil {
		t.Fatal(err)
	}

	errorMessage := "root could not prepare the environment"
	failed := []map[string]any{{
		"environmentId": "remote",
		"cwd":           "/remote",
		"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigFailed, Error: errorMessage}),
	}}
	response := router.Handle(requestWithParams(t, IntID(1), MethodTurnStart, turn.TurnStartParams{ThreadID: "root", Environments: failed}))
	if response.Error != nil {
		t.Fatalf("turn/start error = %+v", response.Error)
	}
	for _, id := range []string{"child", "grandchild"} {
		record, err := store.Load(session.ThreadID(id))
		if err != nil {
			t.Fatal(err)
		}
		selections := environmentSelectionsFromAny(record.Metadata.Extra[runtimeEnvironmentSelectionsExtraKey])
		if len(selections) != 1 {
			t.Fatalf("%s selections = %#v", id, selections)
		}
		state, err := environmentConfigStateFromAnyMap(selections[0])
		if err != nil {
			t.Fatal(err)
		}
		if state.Kind != EnvironmentConfigFailed || state.Error != errorMessage {
			t.Fatalf("%s inherited state = %#v, want the root failure %q", id, selections[0]["config"], errorMessage)
		}
	}
}

// Rust #49075 child configuration precedence: a descendant that resolved its
// own attachment first keeps its configuration, and the owner's later result
// never overwrites the first inherited one.
func TestRuntimeRouterInheritedEnvironmentConfigKeepsChildConfigurationLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	graph := agent.NewMemoryStore()
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "sh"}, "")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "remote", ExecServerURL: "ws://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("ok"),
		ThreadStatus: NewThreadStatusManager(),
		Environment:  manager,
		SpawnGraph:   graph,
	})
	router.SetNotificationSink(NewNotificationBuffer())

	now := time.Now().UTC()
	pending := map[string]any{
		"environmentId": "remote",
		"cwd":           "/remote",
		"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigPending}),
	}
	// The child resolved its own attachment first (owner: false).
	childConfigured := map[string]any{
		"environmentId": "remote",
		"cwd":           "/remote",
		"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigReady, Config: &EnvironmentConfig{AllowLoginShell: true}}),
	}
	root := &session.Record{ID: "root", SessionID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: "/remote", Extra: map[string]any{runtimeEnvironmentSelectionsExtraKey: []map[string]any{cloneAnyMap(pending)}}}}
	if err := store.Create(root); err != nil {
		t.Fatal(err)
	}
	child := &session.Record{ID: "child", SessionID: "child", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: "/remote", Extra: map[string]any{runtimeEnvironmentSelectionsExtraKey: []map[string]any{cloneAnyMap(childConfigured)}}}}
	if err := store.Create(child); err != nil {
		t.Fatal(err)
	}
	if err := graph.UpsertThreadSpawnEdge("root", "child", agent.ThreadSpawnEdgeOpen); err != nil {
		t.Fatal(err)
	}

	resolved := []map[string]any{{
		"environmentId": "remote",
		"cwd":           "/remote",
		"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigReady, Config: &EnvironmentConfig{AllowLoginShell: false}}),
	}}
	response := router.Handle(requestWithParams(t, IntID(1), MethodTurnStart, turn.TurnStartParams{ThreadID: "root", Environments: resolved}))
	if response.Error != nil {
		t.Fatalf("turn/start error = %+v", response.Error)
	}
	record, err := store.Load(session.ThreadID("child"))
	if err != nil {
		t.Fatal(err)
	}
	selections := environmentSelectionsFromAny(record.Metadata.Extra[runtimeEnvironmentSelectionsExtraKey])
	if len(selections) != 1 {
		t.Fatalf("child selections = %#v", selections)
	}
	state, err := environmentConfigStateFromAnyMap(selections[0])
	if err != nil {
		t.Fatal(err)
	}
	if state.Kind != EnvironmentConfigReady || state.Config == nil || !state.Config.AllowLoginShell {
		t.Fatalf("child inherited state = %#v, want the child's own configuration", selections[0]["config"])
	}
}
