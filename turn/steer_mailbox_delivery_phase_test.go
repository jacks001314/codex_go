package turn

import (
	"context"
	"fmt"
	"testing"

	"codex_go/model"
	"codex_go/state"
)

// Rust #48982: a mailbox communication is only accepted while the running turn
// still accepts mailbox delivery
// (InputQueue::deliver_mailbox_communication_to_current_turn). A turn that
// already emitted its final answer (phase NextTurn) must not queue the
// notification.
func TestSteerMailboxGatesDeliveryOnPhaseLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	accepted, err := mailbox.EnqueueIfAcceptingDelivery(&SteerEnqueueParams{
		ThreadID: "t", TurnID: "turn", InputItems: []any{mailboxMailInputItem("/root/worker", "hi")},
	})
	if err != nil || !accepted {
		t.Fatalf("EnqueueIfAcceptingDelivery(current turn) = %v, %v; want true, nil", accepted, err)
	}
	if !mailbox.HasPendingMailboxItems("t", "turn") {
		t.Fatal("an accepted notification was not queued")
	}

	mailbox.SetMailboxDeliveryPhase("t", "closed", state.MailboxNextTurn)
	accepted, err = mailbox.EnqueueIfAcceptingDelivery(&SteerEnqueueParams{
		ThreadID: "t", TurnID: "closed", InputItems: []any{mailboxMailInputItem("/root/worker", "late")},
	})
	if err != nil || accepted {
		t.Fatalf("EnqueueIfAcceptingDelivery(closed turn) = %v, %v; want false, nil", accepted, err)
	}
	if mailbox.HasPendingMailboxItems("t", "closed") || mailbox.HasPending("t", "closed") {
		t.Fatal("a closed turn queued the notification")
	}

	// Explicit same-turn work still reopens the turn for mailbox delivery, so a
	// steered user message continues the turn exactly as before.
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t", TurnID: "closed", InputItems: []any{queuedUserMessage("steer")}}); err != nil {
		t.Fatalf("enqueue steer: %v", err)
	}
	if !mailbox.AcceptsMailboxDeliveryForCurrentTurn("t", "closed") {
		t.Fatal("a steered user message must reopen the current turn")
	}
}

// finalAnswerWithMailAgent delivers inter-agent mail while its sampling request
// is in flight and then answers the request with the given item.
type finalAnswerWithMailAgent struct {
	item                  model.AgentItem
	deliverDuringResponse func(threadID string, turnID string)
	requests              []model.AgentRequest
}

func (a *finalAnswerWithMailAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	if len(a.requests) == 1 && a.deliverDuringResponse != nil {
		a.deliverDuringResponse(request.ThreadID, request.TurnID)
	}
	return &model.AgentResponse{
		ResponseID: fmt.Sprintf("resp-%d", len(a.requests)),
		Message:    a.item.Text,
		Items:      []model.AgentItem{a.item},
	}, nil
}

// Rust #48982 (session/turn.rs::get_pending_input gated on
// accepts_mailbox_delivery_for_current_turn): mail queued while the turn was
// still accepting delivery must not reopen the final answer with one more
// sampling request; it stays in the queue for a later turn.
func TestAgentLoopFinalAnswerDoesNotReopenForQueuedMailLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	agent := &finalAnswerWithMailAgent{
		item:                  model.AgentItem{ID: "final", Type: "agent_message", Text: "Answered.", Data: map[string]any{"phase": "final_answer"}},
		deliverDuringResponse: mailDuringResponse(t, mailbox),
	}
	loop, _ := newMailboxPreemptionHarness(t, agent, mailbox, false)
	result, err := loop.Run(context.Background(), &AgentLoopRequest{Prompt: "answer", Model: "gpt-test", ThreadID: "thread-1", TurnID: "turn-1"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("sampling requests = %d, want 1 (a queued notification reopened the finalized answer)", len(agent.requests))
	}
	if result == nil || result.Response == nil || result.Response.Message != "Answered." {
		t.Fatalf("result = %#v", result)
	}
	if !mailbox.HasPendingMailboxItems("thread-1", "turn-1") {
		t.Fatal("the deferred notification was dropped instead of staying queued")
	}
}

// Control: a non-final response keeps the current-turn phase, so the same mail
// still reaches a follow-up sampling request.
func TestAgentLoopCommentaryStillDrainsQueuedMailLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	agent := &finalAnswerWithMailAgent{
		item:                  model.AgentItem{ID: "commentary", Type: "agent_message", Text: "Working on it.", Data: map[string]any{"phase": "commentary"}},
		deliverDuringResponse: mailDuringResponse(t, mailbox),
	}
	loop, _ := newMailboxPreemptionHarness(t, agent, mailbox, false)
	if _, err := loop.Run(context.Background(), &AgentLoopRequest{Prompt: "answer", Model: "gpt-test", ThreadID: "thread-1", TurnID: "turn-1"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("sampling requests = %d, want 2", len(agent.requests))
	}
	if !itemsCarryAgentMail(agent.requests[1].InputItems) {
		t.Fatalf("queued mail missing from the follow-up request: %v", itemTypes(agent.requests[1].InputItems))
	}
}
