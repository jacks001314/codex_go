package turn

import (
	"context"
	"fmt"
	"testing"

	"codex_go/model"
	"codex_go/state"
	"codex_go/tool"
)

// Rust #49262 (core/src/session/turn.rs::preempt_for_mailbox_mail, extended by
// #51249) and tests/suite/scenarios_mailbox_preemption_tests.rs: at a completed
// commentary or partial-answer boundary, queued inter-agent mail reaches the next
// model request instead of waiting for the response's planned tool calls, and the
// event `codex.mailbox_preemption` is emitted on the sampling request's span.

// mailboxPreemptionLoopAgent returns one boundary item plus a planned echo call
// on its first request, then the turn's final answer.
type mailboxPreemptionLoopAgent struct {
	boundary model.AgentItem
	requests []model.AgentRequest
	// deliverDuringResponse queues the mail the way an asynchronous agent
	// message arrives while the sampling request is in flight.
	deliverDuringResponse func(threadID string, turnID string)
}

func (a *mailboxPreemptionLoopAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	if len(a.requests) == 1 {
		if a.deliverDuringResponse != nil {
			a.deliverDuringResponse(request.ThreadID, request.TurnID)
		}
		return &model.AgentResponse{
			ResponseID: "resp-boundary",
			Items: []model.AgentItem{
				a.boundary,
				{ID: "call-1", Type: "function_call", Name: "echo", CallID: "call-1", Arguments: `{}`},
			},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: fmt.Sprintf("resp-%d", len(a.requests)),
		Message:    "done",
		Items: []model.AgentItem{{
			ID: "final-1", Type: "agent_message", Text: "done", Data: map[string]any{"phase": "final_answer"},
		}},
	}, nil
}

// boundaryItemsLoopAgent returns the given items plus a planned echo call on its
// first request, then the turn's final answer.
type boundaryItemsLoopAgent struct {
	items                 []model.AgentItem
	requests              []model.AgentRequest
	deliverDuringResponse func(threadID string, turnID string)
}

func (a *boundaryItemsLoopAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	if len(a.requests) == 1 {
		if a.deliverDuringResponse != nil {
			a.deliverDuringResponse(request.ThreadID, request.TurnID)
		}
		items := append([]model.AgentItem(nil), a.items...)
		items = append(items, model.AgentItem{ID: "call-1", Type: "function_call", Name: "echo", CallID: "call-1", Arguments: `{}`})
		return &model.AgentResponse{ResponseID: "resp-boundary", Items: items}, nil
	}
	return &model.AgentResponse{
		ResponseID: fmt.Sprintf("resp-%d", len(a.requests)),
		Message:    "done",
		Items: []model.AgentItem{{
			ID: "final-1", Type: "agent_message", Text: "done", Data: map[string]any{"phase": "final_answer"},
		}},
	}, nil
}

// answerOnlyLoopAgent answers the turn with a single assistant message.
type answerOnlyLoopAgent struct {
	item     model.AgentItem
	requests []model.AgentRequest
	mailbox  *SteerMailbox
}

func (a *answerOnlyLoopAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	return &model.AgentResponse{ResponseID: "resp-answer", Message: a.item.Text, Items: []model.AgentItem{a.item}}, nil
}

// recordingMailboxTelemetryTracer implements the turn span tracer plus the
// trace-event sink the loop uses for `codex.mailbox_preemption`.
type recordingMailboxTelemetryTracer struct {
	spans  int
	events []recordedTraceEvent
}

type recordedTraceEvent struct {
	name   string
	fields map[string]string
}

func (t *recordingMailboxTelemetryTracer) StartSpan(ctx context.Context, _ model.TelemetrySpan, _ string, _ map[string]string) (context.Context, model.TelemetrySpan) {
	t.spans++
	return ctx, nil
}

func (t *recordingMailboxTelemetryTracer) EmitTraceSafeEvent(_ context.Context, eventName string, fields map[string]string) {
	t.events = append(t.events, recordedTraceEvent{name: eventName, fields: fields})
}

// mailboxMailInputItem renders inter-agent mail the way the runtime builds it
// (runtimeAgentCommunicationInputItem / execAgentCommunicationInputItem).
func mailboxMailInputItem(author string, text string) any {
	return map[string]any{
		"type":      "agent_message",
		"author":    author,
		"recipient": "/root",
		"content": []any{map[string]any{
			"type": "input_text",
			"text": "Message Type: MESSAGE\nTask name: /root\nSender: " + author + "\nPayload:\n" + text,
		}},
	}
}

// mailDuringResponse returns a hook that queues inter-agent mail while the
// sampling request is in flight, the way a worker's message arrives mid-response.
func mailDuringResponse(t *testing.T, mailbox *SteerMailbox) func(string, string) {
	t.Helper()
	return func(threadID string, turnID string) {
		t.Helper()
		if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: threadID, TurnID: turnID, InputItems: []any{mailboxMailInputItem("/root/worker", "Worker found the result.")}}); err != nil {
			t.Errorf("enqueue mail during response: %v", err)
		}
	}
}

func newMailboxPreemptionHarness(t *testing.T, agent model.AgentRunner, mailbox *SteerMailbox, deferPreemption bool) (*AgentLoop, *int) {
	t.Helper()
	executed := 0
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		executed++
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:                  agent,
		Dispatcher:             NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry), ExecutedToolCalls: NewExecutedToolCallRecorder()}),
		SteerMailbox:           mailbox,
		DeferMailboxPreemption: deferPreemption,
	})
	return loop, &executed
}

func itemsCarryAgentMail(items []any) bool {
	for _, item := range items {
		if raw, ok := item.(map[string]any); ok && raw["type"] == "agent_message" {
			return true
		}
	}
	return false
}

func itemsCarryToolCall(items []any) bool {
	for _, item := range items {
		switch value := item.(type) {
		case *model.AgentItem:
			if value != nil && value.Type == "function_call" {
				return true
			}
		case model.AgentItem:
			if value.Type == "function_call" {
				return true
			}
		}
	}
	return false
}

func itemTypes(items []any) []string {
	types := make([]string, 0, len(items))
	for _, item := range items {
		switch value := item.(type) {
		case *model.AgentItem:
			if value != nil {
				types = append(types, value.Type)
			}
		case *ToolResponseItem:
			types = append(types, value.Type)
		case nil:
			types = append(types, "<nil>")
		default:
			types = append(types, fmt.Sprintf("%T", item))
		}
	}
	return types
}

func TestAgentLoopPreemptsForMailboxMailAtBoundaryLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		boundary model.AgentItem
	}{
		{"commentary", model.AgentItem{ID: "boundary-1", Type: "agent_message", Text: "I will check the worker.", Data: map[string]any{"phase": "commentary"}}},
		{"partial_answer", model.AgentItem{ID: "boundary-2", Type: "agent_message", Text: "Part of the answer.", Data: map[string]any{"phase": "partial_answer"}}},
		{"reasoning", model.AgentItem{ID: "boundary-3", Type: "reasoning"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mailbox := NewSteerMailbox()
			agent := &mailboxPreemptionLoopAgent{boundary: testCase.boundary}
			agent.deliverDuringResponse = mailDuringResponse(t, mailbox)
			loop, executed := newMailboxPreemptionHarness(t, agent, mailbox, false)
			result, err := loop.Run(context.Background(), &AgentLoopRequest{
				Prompt: "plan the next action while the worker finishes", Model: "gpt-test", ThreadID: "thread-1", TurnID: "turn-1",
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if *executed != 0 {
				t.Fatalf("planned tool call ran despite mailbox preemption (%d executions)", *executed)
			}
			if len(result.ToolExecutions) != 0 {
				t.Fatalf("tool executions = %d, want 0", len(result.ToolExecutions))
			}
			if len(agent.requests) != 2 {
				t.Fatalf("sampling requests = %d, want 2 (the turn continues with the queued mail)", len(agent.requests))
			}
			if itemsCarryToolCall(agent.requests[1].InputItems) {
				t.Fatalf("dropped tool call reached the follow-up request: %v", itemTypes(agent.requests[1].InputItems))
			}
			if !itemsCarryAgentMail(agent.requests[1].InputItems) {
				t.Fatalf("queued mail missing from the follow-up request: %v", itemTypes(agent.requests[1].InputItems))
			}
			if result.Iterations != 2 || result.Response == nil || result.Response.Message != "done" {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestAgentLoopKeepsResponseAtNonPreemptingItemsLikeRust(t *testing.T) {
	cases := []struct {
		name  string
		items []model.AgentItem
	}{
		{"final_answer", []model.AgentItem{{ID: "b", Type: "agent_message", Text: "Answered.", Data: map[string]any{"phase": "final_answer"}}}},
		{"untagged", []model.AgentItem{{ID: "b", Type: "agent_message", Text: "Answered."}}},
		{"tool_only", nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mailbox := NewSteerMailbox()
			agent := &boundaryItemsLoopAgent{items: testCase.items}
			agent.deliverDuringResponse = mailDuringResponse(t, mailbox)
			loop, executed := newMailboxPreemptionHarness(t, agent, mailbox, false)
			if _, err := loop.Run(context.Background(), &AgentLoopRequest{Prompt: "run echo", Model: "gpt-test", ThreadID: "thread-1", TurnID: "turn-1"}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if *executed != 1 {
				t.Fatalf("planned tool call executions = %d, want 1 (no preemption at this boundary)", *executed)
			}
			if len(agent.requests) != 2 {
				t.Fatalf("sampling requests = %d, want 2", len(agent.requests))
			}
			if !itemsCarryAgentMail(agent.requests[1].InputItems) {
				t.Fatalf("queued mail missing from the follow-up request: %v", itemTypes(agent.requests[1].InputItems))
			}
		})
	}
}

func TestAgentLoopDefersMailboxPreemptionWhenEnabledLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	agent := &mailboxPreemptionLoopAgent{boundary: model.AgentItem{ID: "boundary-1", Type: "agent_message", Text: "I will check the worker.", Data: map[string]any{"phase": "commentary"}}}
	agent.deliverDuringResponse = mailDuringResponse(t, mailbox)
	loop, executed := newMailboxPreemptionHarness(t, agent, mailbox, true)
	if _, err := loop.Run(context.Background(), &AgentLoopRequest{Prompt: "run echo", Model: "gpt-test", ThreadID: "thread-1", TurnID: "turn-1"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if *executed != 1 {
		t.Fatalf("planned tool call executions = %d, want 1 with defer_mailbox_preemption enabled", *executed)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("sampling requests = %d, want 2", len(agent.requests))
	}
	if !itemsCarryToolCall(agent.requests[1].InputItems) {
		t.Fatalf("tool call missing from the follow-up request: %v", itemTypes(agent.requests[1].InputItems))
	}
	if !itemsCarryAgentMail(agent.requests[1].InputItems) {
		t.Fatalf("queued mail missing from the follow-up request: %v", itemTypes(agent.requests[1].InputItems))
	}
}

func TestAgentLoopEmitsMailboxPreemptionEventLikeRust(t *testing.T) {
	run := func(t *testing.T, deferPreemption bool) *recordingMailboxTelemetryTracer {
		t.Helper()
		mailbox := NewSteerMailbox()
		agent := &mailboxPreemptionLoopAgent{boundary: model.AgentItem{ID: "boundary-1", Type: "agent_message", Text: "I will check the worker.", Data: map[string]any{"phase": "commentary"}}}
		agent.deliverDuringResponse = mailDuringResponse(t, mailbox)
		loop, _ := newMailboxPreemptionHarness(t, agent, mailbox, deferPreemption)
		tracer := &recordingMailboxTelemetryTracer{}
		if _, err := loop.Run(context.Background(), &AgentLoopRequest{
			Prompt: "plan the next action", Model: "gpt-test", ThreadID: "thread-1", TurnID: "turn-1", Tracer: tracer,
		}); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		return tracer
	}

	tracer := run(t, false)
	if len(tracer.events) != 1 {
		t.Fatalf("trace events = %#v, want exactly the mailbox preemption record", tracer.events)
	}
	event := tracer.events[0]
	if event.name != "codex.mailbox_preemption" {
		t.Fatalf("event name = %q, want codex.mailbox_preemption", event.name)
	}
	if event.fields["conversation.id"] != "thread-1" || event.fields["turn.id"] != "turn-1" {
		t.Fatalf("event fields = %#v", event.fields)
	}

	// With the feature enabled the response is kept, so Rust emits no event.
	if events := run(t, true).events; len(events) != 0 {
		t.Fatalf("trace events with defer_mailbox_preemption = %#v, want none", events)
	}
}

func TestAgentLoopDefersMailboxDeliveryForFinalAnswersOnlyLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		phase  string
		text   string
		defers bool
	}{
		{"final_answer", "final_answer", "The answer.", true},
		{"untagged", "", "The answer.", true},
		{"commentary", "commentary", "Working on it.", false},
		{"partial_answer", "partial_answer", "Part of the answer.", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			data := map[string]any{}
			if testCase.phase != "" {
				data["phase"] = testCase.phase
			}
			agent := &answerOnlyLoopAgent{item: model.AgentItem{ID: "msg-1", Type: "agent_message", Text: testCase.text, Data: data}}
			mailbox := NewSteerMailbox()
			loop, _ := newMailboxPreemptionHarness(t, agent, mailbox, false)
			if _, err := loop.Run(context.Background(), &AgentLoopRequest{Prompt: "answer", Model: "gpt-test", ThreadID: "thread-1", TurnID: "turn-1"}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got := mailbox.AcceptsMailboxDeliveryForCurrentTurn("thread-1", "turn-1"); got == testCase.defers {
				t.Fatalf("AcceptsMailboxDeliveryForCurrentTurn = %v, want %v for %s", got, !testCase.defers, testCase.name)
			}
			// Late child mail arriving after the answer was recorded.
			if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "thread-1", TurnID: "turn-1", InputItems: []any{mailboxMailInputItem("/root/child", "late result")}}); err != nil {
				t.Fatalf("enqueue late mail: %v", err)
			}
			if got := mailbox.HasPending("thread-1", "turn-1"); got != !testCase.defers {
				t.Fatalf("HasPending = %v, want %v for %s", got, !testCase.defers, testCase.name)
			}
		})
	}
}

func TestSteerMailboxMailboxDeliveryPredicatesLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	if !mailbox.AcceptsMailboxDeliveryForCurrentTurn("t", "turn") {
		t.Fatal("the default phase is the current turn")
	}
	// A steered user message is not mailbox mail (Rust TurnInput::UserInput).
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn", InputItems: []any{queuedUserMessage("steer")}}); err != nil {
		t.Fatalf("enqueue steer: %v", err)
	}
	if mailbox.HasPendingMailboxItems("t", "turn") {
		t.Fatal("a queued user message must not count as pending mailbox mail")
	}
	if !mailbox.HasPendingUserInput("t", "turn") {
		t.Fatal("queued user input not reported")
	}
	if !mailbox.HasPending("t", "turn") {
		t.Fatal("queued user input not reported as pending")
	}
	// Inter-agent mail and a controller metadata update both count.
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn", InputItems: []any{mailboxMailInputItem("/root/worker", "hi")}}); err != nil {
		t.Fatalf("enqueue mail: %v", err)
	}
	if !mailbox.HasPendingMailboxItems("t", "turn") {
		t.Fatal("queued agent mail not reported")
	}
	// A client-metadata update is queued input but not mailbox mail, so it is not
	// a preemption trigger (Rust's second arm watches the host's retained agent
	// mailbox, which Go tracks outside the turn queue).
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t2", TurnID: "turn", ClientMetadata: map[string]string{"x": "y"}}); err != nil {
		t.Fatalf("enqueue metadata: %v", err)
	}
	if mailbox.HasPendingMailboxItems("t2", "turn") {
		t.Fatal("a client metadata update must not count as pending mailbox mail")
	}
	if !mailbox.HasPending("t2", "turn") {
		t.Fatal("queued client metadata not reported as pending input")
	}
	// The delivery phase gates pending input (Rust InputQueue::has_pending_input).
	mailbox.SetMailboxDeliveryPhase("t", "turn", state.MailboxNextTurn)
	if mailbox.AcceptsMailboxDeliveryForCurrentTurn("t", "turn") {
		t.Fatal("next-turn phase accepted current delivery")
	}
	if mailbox.HasPending("t", "turn") {
		t.Fatal("deferred mailbox delivery must not keep the turn open")
	}
	mailbox.AcceptMailboxDeliveryForCurrentTurn("t", "turn")
	if !mailbox.AcceptsMailboxDeliveryForCurrentTurn("t", "turn") || !mailbox.HasPending("t", "turn") {
		t.Fatal("accepting the current turn did not restore pending input")
	}
	// A steered user message reopens the turn for mailbox delivery.
	mailbox.SetMailboxDeliveryPhase("t", "turn", state.MailboxNextTurn)
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t", TurnID: "turn", InputItems: []any{queuedUserMessage("steer again")}}); err != nil {
		t.Fatalf("enqueue steer: %v", err)
	}
	if !mailbox.AcceptsMailboxDeliveryForCurrentTurn("t", "turn") {
		t.Fatal("a steered user message must reopen the current turn")
	}
}
