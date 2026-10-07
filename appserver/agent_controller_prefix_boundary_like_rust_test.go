package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
)

// TestRuntimeAgentControllerListAgentsPathPrefixRespectsSegmentsLikeRust pins
// the production wiring of agent_matches_prefix: a PathPrefix must end on a
// path segment boundary, so listing under "/root/work" must not return the
// sibling "/root/worker".
func TestRuntimeAgentControllerListAgentsPathPrefixRespectsSegmentsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir()}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	controller, ok := newRuntimeAgentControllerWithVersion(router, "parent", parent.Metadata.CWD, 2, agent.VersionV2).(*runtimeAgentController)
	if !ok {
		t.Fatal("controller is not a runtimeAgentController")
	}
	for _, name := range []string{"work", "worker"} {
		if _, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: name}); err != nil {
			t.Fatalf("spawn %s: %v", name, err)
		}
	}

	agentNames := func(prefix string) []string {
		t.Helper()
		listed, err := controller.ListAgents(context.Background(), &agent.ListAgentsArgs{PathPrefix: &prefix})
		if err != nil {
			t.Fatalf("ListAgents(%q): %v", prefix, err)
		}
		names := make([]string, 0, len(listed.Agents))
		for _, item := range listed.Agents {
			names = append(names, item.AgentName)
		}
		return names
	}

	if got := agentNames("work"); len(got) != 1 || got[0] != "/root/work" {
		t.Fatalf("ListAgents(PathPrefix=\"work\") = %#v, want [/root/work] (the sibling /root/worker must not match)", got)
	}
	// The root prefix still matches everything, mirroring prefix.is_root().
	got := agentNames("/root")
	if len(got) != 3 {
		t.Fatalf("ListAgents(PathPrefix=\"/root\") = %#v, want all three agents", got)
	}
	seen := map[string]bool{}
	for _, name := range got {
		seen[name] = true
	}
	for _, want := range []string{"/root", "/root/work", "/root/worker"} {
		if !seen[want] {
			t.Fatalf("ListAgents(PathPrefix=\"/root\") = %#v, missing %q", got, want)
		}
	}
}
