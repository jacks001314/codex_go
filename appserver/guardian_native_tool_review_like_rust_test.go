package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
	"codex_go/tool"
)

func nativeToolReviewRouter(t *testing.T, items ...session.Item) *RuntimeRouter {
	t.Helper()
	home := t.TempDir()
	store := session.NewStore(home)
	now := time.Date(2026, 7, 31, 8, 0, 2, 0, time.UTC)
	if err := store.Create(&session.Record{
		ID:        session.ThreadID("native-review-thread"),
		SessionID: "native-review-thread",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Items:     items,
	}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	return NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
}

func nativeToolReviewInvocation(arguments string) *tool.Invocation {
	return nativeToolReviewInvocationWithCall(arguments, nil)
}

func nativeToolReviewInvocationWithCall(arguments string, nativeCall json.RawMessage) *tool.Invocation {
	return &tool.Invocation{
		CallID:     "action",
		ToolName:   tool.NamespacedName("team", "send_message"),
		Payload:    tool.Payload{Kind: tool.PayloadFunction, Arguments: arguments},
		Source:     "direct_plaintext_message",
		NativeCall: nativeCall,
	}
}

// Mirrors Rust #58040: the call retained by dispatch is the review evidence,
// so it survives even when the live history no longer holds the call. A hook
// rewrite keeps the call identity and encrypted-argument metadata while
// dropping the stale replay content.
func TestNativeToolGuardianActionPreservesEncryptedReplayLikeRust(t *testing.T) {
	recorded := json.RawMessage(`{"type":"function_call","id":"fc_action","call_id":"action","namespace":"team","name":"send_message","arguments":"{\"target\":\"worker\",\"message\":\"opaque-message\"}","encrypted_content":"opaque-replay","encrypted_function_args":["message"]}`)
	// No persisted history: the retained call alone carries the evidence.
	router := nativeToolReviewRouter(t)

	action, err := router.nativeToolGuardianAction("native-review-thread", nativeToolReviewInvocationWithCall(`{"target":"worker","message":"opaque-message"}`, recorded))
	if err != nil {
		t.Fatalf("nativeToolGuardianAction() error = %v", err)
	}
	if action.Type != state.ActionNativeToolCall || !strings.Contains(string(action.NativeCall), `"opaque-replay"`) {
		t.Fatalf("action = %#v, want the retained call with encrypted content", action)
	}

	// A hook rewrite reviews the updated arguments and discards the original
	// encrypted replay content, which no longer describes the call.
	action, err = router.nativeToolGuardianAction("native-review-thread", nativeToolReviewInvocationWithCall(`{"target":"/root","message":"opaque-message"}`, recorded))
	if err != nil {
		t.Fatalf("nativeToolGuardianAction(rewritten) error = %v", err)
	}
	var reviewed map[string]any
	if err := json.Unmarshal(action.NativeCall, &reviewed); err != nil {
		t.Fatalf("unmarshal reviewed call: %v", err)
	}
	if reviewed["arguments"] != `{"target":"/root","message":"opaque-message"}` {
		t.Fatalf("reviewed arguments = %v", reviewed["arguments"])
	}
	if _, ok := reviewed["encrypted_content"]; ok {
		t.Fatalf("rewritten call kept encrypted replay content: %s", action.NativeCall)
	}
	if _, ok := reviewed["encrypted_function_args"]; !ok {
		t.Fatalf("rewritten call dropped the encrypted argument fields: %s", action.NativeCall)
	}
	if reviewed["call_id"] != "action" || reviewed["name"] != "send_message" || reviewed["namespace"] != "team" {
		t.Fatalf("rewritten call lost its identity: %s", action.NativeCall)
	}

	// Without a retained call the review synthesizes one from the invocation;
	// plaintext sources carry an empty encrypted-argument list.
	action, err = router.nativeToolGuardianAction("native-review-thread", nativeToolReviewInvocation(`{"target":"/root"}`))
	if err != nil {
		t.Fatalf("nativeToolGuardianAction(synthesized) error = %v", err)
	}
	var synthesized map[string]any
	if err := json.Unmarshal(action.NativeCall, &synthesized); err != nil {
		t.Fatalf("unmarshal synthesized call: %v", err)
	}
	if synthesized["type"] != "function_call" || synthesized["call_id"] != "action" || synthesized["name"] != "send_message" || synthesized["namespace"] != "team" {
		t.Fatalf("synthesized call = %s", action.NativeCall)
	}
	if list, ok := synthesized["encrypted_function_args"].([]any); !ok || len(list) != 0 {
		t.Fatalf("synthesized encrypted_function_args = %v, want an empty list", synthesized["encrypted_function_args"])
	}

	// Non-function payloads cannot be reviewed natively.
	if _, err := router.nativeToolGuardianAction("native-review-thread", &tool.Invocation{CallID: "action", ToolName: tool.PlainName("x"), Payload: tool.Payload{Kind: tool.PayloadCustom}}); err == nil {
		t.Fatal("a custom payload was accepted for native review")
	}
}

// Mirrors Rust #57433: a native tool call review keeps accounting and evidence
// but publishes no client assessment events, and the proposed call is not
// duplicated into the transcript.
func TestNativeToolGuardianReviewSuppressesClientEventsLikeRust(t *testing.T) {
	var captured *model.AgentRequest
	var events []state.Event
	var assessed []string
	call := json.RawMessage(`{"type":"function_call","id":"fc_action","call_id":"action","namespace":"team","name":"send_message","arguments":"{\"target\":\"/root\"}"}`)
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			captured = request
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store: state.NewReviewStore(),
		notify: func(_ string, event *state.Event) {
			events = append(events, *event)
		},
		logAssessment: func(_ string, event *state.Event) {
			assessed = append(assessed, event.ID)
		},
		transcriptSelection: func(string) guardianTranscriptSelection {
			return guardianTranscriptSelection{Entries: []state.GuardianTranscriptEntry{
				{Index: 0, Author: "user", LineRole: "user", Text: "Please send it."},
				{Index: 1, Label: "tool send_message call", Native: call},
			}}
		},
	}
	decision, reason, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "action", state.Action{
		Type: state.ActionNativeToolCall, NativeCall: call,
	})
	if err != nil || decision != state.DecisionApproved {
		t.Fatalf("Review() = %v %q %v, want approved", decision, reason, err)
	}
	if len(events) != 0 {
		t.Fatalf("native review emitted client events: %#v", events)
	}
	if len(assessed) != 0 {
		t.Fatalf("native review logged an assessment event: %#v", assessed)
	}
	if captured == nil {
		t.Fatal("the reviewer never ran")
	}
	if !strings.Contains(captured.Prompt, `The native tool call with call ID "action" is the exact proposed action under review.`) {
		t.Fatalf("prompt missing the native framing:\n%s", captured.Prompt)
	}
	if !strings.Contains(captured.Prompt, state.GuardianTranscriptOmissionNotice) {
		t.Fatalf("prompt missing the transcript omission placeholder:\n%s", captured.Prompt)
	}
	if strings.Contains(captured.Prompt, `"target":"/root"`) {
		t.Fatalf("prompt repeated the proposed call as text:\n%s", captured.Prompt)
	}
	var calls, outputs int
	for _, item := range captured.InputItems {
		data, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("marshal input item: %v", err)
		}
		switch {
		case strings.Contains(string(data), `"type":"function_call"`):
			calls++
		case strings.Contains(string(data), `"type":"function_call_output"`):
			outputs++
		}
	}
	if calls != 1 || outputs != 1 {
		t.Fatalf("native evidence items = %d calls, %d outputs; want exactly the proposed call and its evidence output", calls, outputs)
	}
}

type nativeToolReviewStubReviewer struct {
	decision state.ReviewDecision
	reason   string
	err      error
	actions  []state.Action
}

func (r *nativeToolReviewStubReviewer) Review(_ context.Context, _, _, _ string, action state.Action) (state.ReviewDecision, string, error) {
	r.actions = append(r.actions, action)
	return r.decision, r.reason, r.err
}

// Rust #57433: native tool review has no user-approval fallback, so a denial, a
// cancellation and an unavailable reviewer all stop dispatch with a
// model-facing message.
func TestNativeToolGuardianReviewFailsClosedLikeRust(t *testing.T) {
	invocation := nativeToolReviewInvocation(`{}`)
	tests := []struct {
		name     string
		reviewer GuardianReviewer
		want     string
	}{
		{name: "denied", reviewer: &nativeToolReviewStubReviewer{decision: state.DecisionDenied, reason: "review denied"}, want: "review denied"},
		{name: "aborted", reviewer: &nativeToolReviewStubReviewer{decision: state.DecisionAborted}, want: "automatic approval review was cancelled"},
		{name: "timed out", reviewer: &nativeToolReviewStubReviewer{decision: state.DecisionTimedOut, reason: "review timed out"}, want: "review timed out"},
		{name: "unavailable", reviewer: nil, want: "Auto-approval review is unavailable; the request was denied."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := nativeToolReviewRouter(t)
			router.services.GuardianReviewer = test.reviewer
			err := router.nativeToolGuardianReview("native-review-thread", "turn-1")(context.Background(), invocation)
			var callErr *tool.FunctionCallError
			if !tool.AsFunctionCallError(err, &callErr) || !callErr.RespondsToModel() || callErr.ModelMessage() != test.want {
				t.Fatalf("review error = %v, want %q", err, test.want)
			}
		})
	}

	approving := &nativeToolReviewStubReviewer{decision: state.DecisionApproved}
	router := nativeToolReviewRouter(t)
	router.services.GuardianReviewer = approving
	if err := router.nativeToolGuardianReview("native-review-thread", "turn-1")(context.Background(), invocation); err != nil {
		t.Fatalf("approved review error = %v", err)
	}
	if len(approving.actions) != 1 || approving.actions[0].Type != state.ActionNativeToolCall {
		t.Fatalf("reviewed actions = %#v, want one native tool call", approving.actions)
	}
	var reviewed map[string]any
	if err := json.Unmarshal(approving.actions[0].NativeCall, &reviewed); err != nil {
		t.Fatalf("unmarshal reviewed call: %v", err)
	}
	if reviewed["type"] != "function_call" || reviewed["call_id"] != "action" || reviewed["arguments"] != "{}" {
		t.Fatalf("reviewed call = %s", approving.actions[0].NativeCall)
	}
}

// Rust GuardianReviewedAction::NativeToolCall serializes as `native_tool_call`
// for review metrics.
func TestGuardianReviewedActionTagNativeToolCallLikeRust(t *testing.T) {
	if got := guardianReviewedActionTag(state.Action{Type: state.ActionNativeToolCall}); got != "native_tool_call" {
		t.Fatalf("native tool call reviewed-action tag = %q", got)
	}
}
