package exec

import (
	"context"
	"testing"

	"codex_go/agent"
)

// TestExecAgentControllerListAgentsPathPrefixRespectsSegmentsLikeRust pins the
// exec-side wiring of agent_matches_prefix: listing under "/root/work" must
// not return the sibling "/root/worker".
func TestExecAgentControllerListAgentsPathPrefixRespectsSegmentsLikeRust(t *testing.T) {
	controller := &execAgentController{tasks: map[string]*execAgentTask{
		"work":   {id: "work", taskName: "work", path: "/root/work"},
		"worker": {id: "worker", taskName: "worker", path: "/root/worker"},
	}}
	names := func(prefix string) []string {
		t.Helper()
		listed, err := controller.ListAgents(context.Background(), &agent.ListAgentsArgs{PathPrefix: &prefix})
		if err != nil {
			t.Fatalf("ListAgents(%q): %v", prefix, err)
		}
		out := make([]string, 0, len(listed.Agents))
		for _, item := range listed.Agents {
			out = append(out, item.AgentName)
		}
		return out
	}

	if got := names("/root/work"); len(got) != 1 || got[0] != "/root/work" {
		t.Fatalf("ListAgents(PathPrefix=\"/root/work\") = %#v, want [/root/work] (the sibling /root/worker must not match)", got)
	}
	got := names("/root")
	if len(got) != 3 {
		t.Fatalf("ListAgents(PathPrefix=\"/root\") = %#v, want all three agents", got)
	}
}
