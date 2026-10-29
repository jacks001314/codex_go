package agent

import (
	"context"
	"encoding/json"
	"testing"

	"codex_go/tool"
)

// TestListedAgentsFromLiveFallsBackToThreadIDLikeRust pins Rust #46553: the
// control plane returns LiveAgent records and the list_agents handler keeps the
// agent path as agent_name, falling back to the thread id for an agent without
// a path.
func TestListedAgentsFromLiveFallsBackToThreadIDLikeRust(t *testing.T) {
	live := []LiveAgent{
		{ThreadID: "thread-2", Metadata: Metadata{ThreadID: "thread-2"}, Status: AgentMessageStatus{Kind: AgentMessageStatusRunning}},
		{ThreadID: "thread-1", Metadata: Metadata{ThreadID: "thread-1", Path: "/root/worker"}, Status: AgentMessageStatus{Kind: AgentMessageStatusCompleted, Message: "done"}},
	}
	listed := ListedAgentsFromLive(live)
	if len(listed) != 2 {
		t.Fatalf("listed = %#v, want two agents", listed)
	}
	if listed[0].AgentName != "/root/worker" || listed[1].AgentName != "thread-2" {
		t.Fatalf("listed names = %q/%q, want /root/worker and the thread id fallback thread-2", listed[0].AgentName, listed[1].AgentName)
	}
	completed, err := json.Marshal(listed[0].AgentStatus)
	if err != nil || string(completed) != `{"completed":"done"}` {
		t.Fatalf("completed status = %s, %v", completed, err)
	}
	fallback, err := json.Marshal(listed[1].AgentStatus)
	if err != nil || string(fallback) != `"running"` {
		t.Fatalf("running status = %s, %v", fallback, err)
	}
}

// TestListAgentsHandlerFormatsLiveRecordsLikeRust checks the whole list_agents
// path: the controller hands back live records and the handler serialises the
// unchanged response fields.
func TestListAgentsHandlerFormatsLiveRecordsLikeRust(t *testing.T) {
	controller := NewMemoryToolController()
	controller.SetStatus("worker", AgentMessageStatus{Kind: AgentMessageStatusRunning})
	registry := tool.NewRegistry()
	if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller: controller,
		Version:    VersionV2,
		Namespace:  MultiAgentV2Namespace,
	}); err != nil {
		t.Fatalf("RegisterMultiAgentHandlersWithOptions() error = %v", err)
	}
	list, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, "list_agents"))
	if !ok {
		t.Fatal("list_agents was not registered")
	}
	output, err := list.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{
		Kind: tool.PayloadFunction, Arguments: `{}`,
	}})
	if err != nil {
		t.Fatalf("list_agents error = %v", err)
	}
	var payload struct {
		Agents []struct {
			AgentName   string `json:"agent_name"`
			AgentStatus any    `json:"agent_status"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(output.Body), &payload); err != nil {
		t.Fatalf("list_agents body %q is not JSON: %v", output.Body, err)
	}
	names := make([]string, 0, len(payload.Agents))
	for _, item := range payload.Agents {
		names = append(names, item.AgentName)
	}
	if len(names) != 2 || names[0] != "/root" || names[1] != "/root/worker" {
		t.Fatalf("agent names = %#v, want [/root /root/worker]", names)
	}
	if status, _ := json.Marshal(payload.Agents[1].AgentStatus); string(status) != `"running"` {
		t.Fatalf("agent status = %s", status)
	}
}
