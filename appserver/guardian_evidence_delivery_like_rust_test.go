package appserver

import (
	"context"
	"strings"
	"sync"
	"testing"

	"codex_go/agentboard"
	"codex_go/model"
	"codex_go/state"
)

// Rust parity: codex-rs/ext/guardian-v3/src/actor.rs + conversation.rs (#62136).
// Evidence routed to an actor waits in its inbox until the actor's reviewer is
// idle or about to decide; each queued original reaches one reviewer
// conversation once, and a replacement reviewer replays the inbox.
func TestGuardianReviewerDeliversQueuedEvidenceLikeRust(t *testing.T) {
	cwd := t.TempDir()
	var mu sync.Mutex
	var prompts []string
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		mu.Lock()
		prompts = append(prompts, request.Prompt)
		mu.Unlock()
		return &model.AgentResponse{
			ResponseID: "resp",
			Message:    `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`,
		}, nil
	})
	reviewer := newGuardianActorReviewer(t, agent)
	evidence := `{"sources":[{"id":{"ordinal":7},"original":{"type":"function_call_output","output":"routed payload"}}]}`
	queue := []agentboard.GuardianEvidenceItem{{Name: "guardian_original_evidence", Namespace: "guardian", Text: evidence}}
	reviewer.evidenceDelivery = func(_ context.Context, threadID string, delivered int) ([]agentboard.GuardianEvidenceItem, int, error) {
		if threadID != "thread-root" || delivered >= len(queue) {
			return nil, delivered, nil
		}
		return queue[delivered:], len(queue), nil
	}
	action := state.Action{Type: "command", Command: "echo hi", CWD: cwd}

	reviewGuardianActor(t, reviewer, "thread-root", action)
	reviewGuardianActor(t, reviewer, "thread-root", action)

	mu.Lock()
	first, second := prompts[0], prompts[1]
	mu.Unlock()
	if !strings.Contains(first, "routed payload") {
		t.Fatalf("first review did not receive the queued evidence: %q", first)
	}
	if strings.Contains(second, "routed payload") {
		t.Fatalf("reused reviewer received the same original twice: %q", second)
	}

	// A replacement reviewer replays the inbox: the delivery cursor belongs to
	// the conversation that was retired.
	reviewer.DropThreadSession("thread-root")
	reviewGuardianActor(t, reviewer, "thread-root", action)
	mu.Lock()
	third := prompts[2]
	mu.Unlock()
	if !strings.Contains(third, "routed payload") {
		t.Fatalf("replacement reviewer did not replay the inbox: %q", third)
	}
}
