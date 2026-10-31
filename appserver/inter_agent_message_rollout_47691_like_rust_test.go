package appserver

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/model"
	"codex_go/rollout"
)

// TestInterAgentCommunicationTurnMaterializesRolloutLikeRust mirrors Rust
// #47691: a first turn started by an inter-agent communication materializes
// the thread's rollout before the first sampling request, even though the turn
// carries no user prompt of its own.
func TestInterAgentCommunicationTurnMaterializesRolloutLikeRust(t *testing.T) {
	router, cwd := newAgentMessageWakeTestRouter(t, true)
	runner, ok := router.services.Agent.(*blockingReviewRuntimeAgent)
	if !ok || runner == nil {
		t.Fatalf("test agent = %T, want blockingReviewRuntimeAgent", router.services.Agent)
	}
	controller, ok := newRuntimeAgentControllerWithVersion(router, "child", cwd, 4, agent.VersionV2).(agent.V2ToolController)
	if !ok {
		t.Fatal("runtime controller does not implement V2ToolController")
	}
	if err := controller.SendMessage(context.Background(), &agent.SendMessageArgs{
		Target:    "/root",
		Message:   "first prompt",
		Plaintext: true,
	}); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	active := router.threads.ActiveTurn("root")
	if active == nil || active.Params == nil {
		t.Fatal("inter-agent communication did not start the root turn")
	}
	defer router.cancelActiveRuntimeTurn("root", active.TurnID)
	var request model.AgentRequest
	select {
	case request = <-runner.requests:
	case <-time.After(10 * time.Second):
		t.Fatal("first sampling request never started")
	}
	path, err := rollout.FindThreadPath(router.codexHomeForRollout(), "root", false)
	if err != nil || strings.TrimSpace(path) == "" {
		t.Fatalf("rollout was not materialized before sampling: path=%q err=%v", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	startedForTurn := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event struct {
			Payload struct {
				Type      string `json:"type"`
				TurnID    string `json:"turn_id"`
				TurnIDAlt string `json:"turnId"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		// Go records the turn boundary as `task_started`; Rust calls the same
		// event `turn_started`.
		if event.Payload.Type == "task_started" && firstNonEmpty(event.Payload.TurnID, event.Payload.TurnIDAlt) == active.TurnID {
			startedForTurn = true
			break
		}
	}
	if !startedForTurn {
		t.Fatalf("rollout %q has no task_started for %q: %s", path, active.TurnID, string(data))
	}
	var communication map[string]any
	for _, item := range request.InputItems {
		message, ok := item.(map[string]any)
		if !ok || message["type"] != "agent_message" {
			continue
		}
		communication = message
		break
	}
	if communication == nil {
		t.Fatalf("sampling input = %#v, want the accepted agent communication", request.InputItems)
	}
	if communication["author"] != "/root/worker" || communication["recipient"] != "/root" {
		t.Fatalf("accepted communication route = %v -> %v, want /root/worker -> /root", communication["author"], communication["recipient"])
	}
}
