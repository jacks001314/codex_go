package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
	"codex_go/turn"
)

// TestRuntimeAgentControllerSpawnInheritsUsageAttributionLikeRust pins Rust
// #63219: a thread started with experimental raw events keeps per-item usage
// attribution, and subagents inherit that policy on both fresh and forked
// spawns so their Responses requests also send `x-codex-exclude: tools`.
func TestRuntimeAgentControllerSpawnInheritsUsageAttributionLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		parentRetains bool
		forkTurns     string
	}{
		{name: "fresh child of raw-event parent", parentRetains: true, forkTurns: "none"},
		{name: "forked child of raw-event parent", parentRetains: true, forkTurns: "all"},
		{name: "plain parent stays excluded", parentRetains: false, forkTurns: "none"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := session.NewStore(t.TempDir())
			now := time.Now().UTC()
			extra := map[string]any{}
			if testCase.parentRetains {
				extra[experimentalRawEventsExtraKey] = true
			}
			parent := &session.Record{
				ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
				Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4", ModelProvider: "openai", Extra: extra},
			}
			if err := store.Create(parent); err != nil {
				t.Fatal(err)
			}
			router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
			defer router.Close()
			if err := router.threads.RegisterTurn("parent", "parent-turn", func() {}, now.UnixMilli(), &turn.TurnStartParams{ThreadID: "parent"}); err != nil {
				t.Fatal(err)
			}
			controller := newRuntimeAgentControllerForTurn(router, "parent", "parent-turn", "root-turn", "", "", parent.Metadata.CWD, 4, agent.VersionV2, nil).(*runtimeAgentController)
			message := "do the work"
			forkTurns := testCase.forkTurns
			child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "worker", Message: &message, ForkTurns: &forkTurns})
			if err != nil {
				t.Fatal(err)
			}
			childRecord, err := store.Read(session.ThreadID(child.AgentID), true, false)
			if err != nil {
				t.Fatal(err)
			}
			if got := threadRecordExperimentalRawEvents(childRecord); got != testCase.parentRetains {
				t.Fatalf("child raw events = %t, want %t (extra=%#v)", got, testCase.parentRetains, childRecord.Metadata.Extra)
			}
			active := router.threads.ActiveTurn(child.AgentID)
			if active == nil || active.Params == nil {
				t.Fatalf("child turn was not started: %#v", active)
			}
			if got := active.Params.ExperimentalRawEvents; got != testCase.parentRetains {
				t.Fatalf("child turn raw events = %t, want the inherited %t", got, testCase.parentRetains)
			}
			// A resumed child restores the policy from its own record, which is
			// what its next turn sees after the original runtimes unload.
			resumed := &turn.TurnStartParams{ThreadID: child.AgentID}
			if err := router.prepareTurnStartParams(resumed); err != nil {
				t.Fatalf("prepareTurnStartParams() error = %v", err)
			}
			if resumed.ExperimentalRawEvents != testCase.parentRetains {
				t.Fatalf("resumed child raw events = %t, want %t", resumed.ExperimentalRawEvents, testCase.parentRetains)
			}
		})
	}
}
