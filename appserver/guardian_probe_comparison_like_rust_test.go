package appserver

// Mirrors Rust #62571: a streamed serving flag is an observation. It is matched
// to one response item, carried into the Code Mode calls that item encloses,
// and logged beside the tool and synchronous review outcomes that share its
// action id. Async scoring stays authoritative in Go, which has no async scorer
// to bypass.

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/state"
	"codex_go/tool"
)

func TestGuardianProbeComparisonToolStageFieldsLikeRust(t *testing.T) {
	direct := &tool.Invocation{CallID: "call-1"}
	key, scope := guardianProbeKey(direct)
	if key != "call-1" || scope != "tool_item" {
		t.Fatalf("direct key = %q scope = %q", key, scope)
	}
	nested := &tool.Invocation{
		CallID:  "call-2",
		Source:  tool.InvocationSourceCodeMode,
		Context: map[string]any{tool.CodeModeCellIDContextKey: "cell-1"},
	}
	key, scope = guardianProbeKey(nested)
	if key != "cell-1" || scope != "enclosing_item" {
		t.Fatalf("code mode key = %q scope = %q", key, scope)
	}
	fields := guardianProbeToolStageFields("thread-1", "turn-1", "call-1", "gpt-5", state.GuardianContextModeIndependent, scope, "not_required")
	for name, want := range map[string]string{
		"stage": "tool", "thread_id": "thread-1", "turn_id": "turn-1", "call_id": "call-1",
		"model": "gpt-5", "guardian_context_mode": "independent", "scope": "enclosing_item", "probe_review": "not_required",
	} {
		if fields[name] != want {
			t.Fatalf("field %q = %q, want %q", name, fields[name], want)
		}
	}
	if got := guardianProbeToolStageFields("thread-1", "turn-1", "call-1", "", state.GuardianContextMode(""), "tool_item", "not_reported"); got["guardian_context_mode"] != "not_reported" {
		t.Fatalf("unknown context mode = %q", got["guardian_context_mode"])
	}
	if guardianProbeToolStageFields("", "turn-1", "call-1", "", state.GuardianContextModeIndependent, "tool_item", "not_reported") != nil {
		t.Fatal("a record without a thread id was emitted")
	}
}

func TestGuardianProbeCarriedToCodeModeCallLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, nil); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	router.recordGuardianServingAssessment("thread-1", "turn-1", "cell-1", state.ServingAssessment{RequiresReview: false})
	nested := &tool.Invocation{
		CallID:  "nested-call",
		Source:  tool.InvocationSourceCodeMode,
		Context: map[string]any{tool.CodeModeCellIDContextKey: "cell-1"},
	}
	router.carryGuardianProbeToCodeModeCall("thread-1", "turn-1", nested)
	carried := router.guardianServingAssessmentFor("thread-1", "turn-1", "nested-call")
	if carried == nil || carried.RequiresReview {
		t.Fatalf("nested call probe = %#v", carried)
	}

	direct := &tool.Invocation{CallID: "direct-call"}
	router.carryGuardianProbeToCodeModeCall("thread-1", "turn-1", direct)
	if router.guardianServingAssessmentFor("thread-1", "turn-1", "direct-call") != nil {
		t.Fatal("a direct call received an enclosing item's probe")
	}

	// A value recorded for the call itself wins over the enclosing cell.
	router.recordGuardianServingAssessment("thread-1", "turn-1", "nested-call", state.ServingAssessment{RequiresReview: true})
	router.carryGuardianProbeToCodeModeCall("thread-1", "turn-1", nested)
	if own := router.guardianServingAssessmentFor("thread-1", "turn-1", "nested-call"); own == nil || !own.RequiresReview {
		t.Fatalf("own probe = %#v, want it preserved", own)
	}
}

func TestGuardianProbeClearedWhenResponseStartsLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	stream := &responsesStreamNotificationState{}
	router.notifyResponsesStreamEvent("thread-1", "turn-1", &model.ResponsesStreamEvent{
		Kind:               model.ResponsesStreamEventGuardianAssessment,
		GuardianAssessment: &model.ResponsesGuardianAssessment{ItemID: "item-1", RequiresReview: false},
	}, stream)
	if stream.pendingGuardianAssessment == nil {
		t.Fatal("the serving flag was not retained for its item")
	}
	router.notifyResponsesStreamEvent("thread-1", "turn-1", &model.ResponsesStreamEvent{
		Kind:       model.ResponsesStreamEventCreated,
		ResponseID: "resp-2",
	}, stream)
	if stream.pendingGuardianAssessment != nil {
		t.Fatalf("pending probe survived a new response: %#v", stream.pendingGuardianAssessment)
	}
}

func TestGuardianSyncProbeComparisonLoggedLikeRust(t *testing.T) {
	var records []map[string]string
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store:          state.NewReviewStore(),
		probeAssessment: func(string, string, string) *state.ServingAssessment {
			return &state.ServingAssessment{RequiresReview: false}
		},
		probeLog: func(_ string, fields map[string]string) { records = append(records, fields) },
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "command", Command: "ls", CWD: "/repo"})
	if err != nil || decision != state.DecisionApproved {
		t.Fatalf("Review() = %q, %v", decision, err)
	}
	if len(records) != 1 {
		t.Fatalf("comparison records = %d, want 1", len(records))
	}
	record := records[0]
	for name, want := range map[string]string{
		"stage": "sync_guardian", "call_id": "call-1", "probe_review": "not_required",
		"sync_outcome": "completed", "sync_decision": "allow",
	} {
		if record[name] != want {
			t.Fatalf("record %q = %q, want %q", name, record[name], want)
		}
	}
}
