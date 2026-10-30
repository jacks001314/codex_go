package appserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

func servingAssessmentTestRouter(t *testing.T, threadID, turnID string) *RuntimeRouter {
	t.Helper()
	router := NewRuntimeRouter(RuntimeServices{})
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}
	return router
}

// Mirrors Rust #58075: the final serving value is matched to the response and
// output item it was emitted for, then retained with that invocation.
func TestGuardianServingAssessmentMatchesResponseItemLikeRust(t *testing.T) {
	const threadID, turnID = "thread-serving", "turn-serving"
	router := servingAssessmentTestRouter(t, threadID, turnID)
	snapshot := newResponsesStreamNotificationState(false, turnID)
	router.notifyResponsesStreamEvent(threadID, turnID, &model.ResponsesStreamEvent{
		Kind:               model.ResponsesStreamEventGuardianAssessment,
		ResponseID:         "resp-serving",
		GuardianAssessment: &model.ResponsesGuardianAssessment{ResponseID: "resp-serving", ItemID: "call-serving"},
	}, snapshot)
	if got := router.guardianServingAssessmentFor(threadID, turnID, "call-serving"); got != nil {
		t.Fatalf("assessment retained before its output item completed: %#v", got)
	}
	router.notifyResponsesStreamEvent(threadID, turnID, &model.ResponsesStreamEvent{
		Kind:       model.ResponsesStreamEventOutputDone,
		ResponseID: "resp-serving",
		ItemID:     "call-serving",
		Item:       &model.AgentItem{ID: "call-serving", Type: "function_call", Name: "send_message", CallID: "call-serving", Arguments: "{}"},
	}, snapshot)
	if got := router.guardianServingAssessmentFor(threadID, turnID, "call-serving"); got == nil || got.RequiresReview {
		t.Fatalf("retained assessment = %#v, want a low-risk value", got)
	}

	// A value emitted for one item never attaches to a different item.
	router.notifyResponsesStreamEvent(threadID, turnID, &model.ResponsesStreamEvent{
		Kind:               model.ResponsesStreamEventGuardianAssessment,
		ResponseID:         "resp-serving",
		GuardianAssessment: &model.ResponsesGuardianAssessment{ResponseID: "resp-serving", ItemID: "call-other"},
	}, snapshot)
	router.notifyResponsesStreamEvent(threadID, turnID, &model.ResponsesStreamEvent{
		Kind:       model.ResponsesStreamEventOutputDone,
		ResponseID: "resp-serving",
		ItemID:     "call-unrelated",
		Item:       &model.AgentItem{ID: "call-unrelated", Type: "function_call", CallID: "call-unrelated"},
	}, snapshot)
	if got := router.guardianServingAssessmentFor(threadID, turnID, "call-unrelated"); got != nil {
		t.Fatalf("assessment leaked onto an unrelated item: %#v", got)
	}

	// An elevated-risk value is retained too; the reviewer decides what it means.
	router.notifyResponsesStreamEvent(threadID, turnID, &model.ResponsesStreamEvent{
		Kind:               model.ResponsesStreamEventGuardianAssessment,
		ResponseID:         "resp-serving",
		GuardianAssessment: &model.ResponsesGuardianAssessment{ResponseID: "resp-serving", ItemID: "call-elevated", RequiresReview: true},
	}, snapshot)
	router.notifyResponsesStreamEvent(threadID, turnID, &model.ResponsesStreamEvent{
		Kind:       model.ResponsesStreamEventOutputDone,
		ResponseID: "resp-serving",
		ItemID:     "call-elevated",
		Item:       &model.AgentItem{ID: "call-elevated", Type: "function_call", CallID: "call-elevated"},
	}, snapshot)
	if got := router.guardianServingAssessmentFor(threadID, turnID, "call-elevated"); got == nil || !got.RequiresReview {
		t.Fatalf("retained elevated assessment = %#v", got)
	}
}

func servingAssessmentReviewer(t *testing.T, assessment *state.ServingAssessment, current state.ServingAuthorization) (*modelGuardianReviewer, *int) {
	t.Helper()
	calls := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store: state.NewReviewStore(),
		servingAssessment: func(threadID, turnID, callID string) *state.ServingAssessment {
			if assessment != nil && callID == "call-serving" {
				return assessment
			}
			return nil
		},
		servingAuthorization: func(threadID, turnID string) state.ServingAuthorization { return current },
	}
	return reviewer, &calls
}

// Mirrors Rust #58075: a matching low-risk serving assessment replaces the
// synchronous review, while elevated risk, stale authorization, and an
// explicit fresh-review requirement still reach the reviewer.
func TestGuardianReviewUsesServingAssessmentLikeRust(t *testing.T) {
	current := state.ServingAuthorization{RetainedContextComplete: true, PolicyFingerprint: "model|policy"}
	lowRisk := &state.ServingAssessment{Authorization: current}
	reviewer, calls := servingAssessmentReviewer(t, lowRisk, current)
	decision, reason, err := reviewer.Review(context.Background(), "thread-serving-review", "turn-serving-review", "call-serving", state.Action{Type: "command", Command: "ls", CWD: "/tmp"})
	if err != nil || decision != state.DecisionApproved {
		t.Fatalf("Review() = %v, %q, %v; want approved", decision, reason, err)
	}
	if *calls != 0 {
		t.Fatalf("reviewer sampled despite a low-risk serving assessment (%d calls)", *calls)
	}

	// Elevated risk requires the full review.
	reviewer, calls = servingAssessmentReviewer(t, &state.ServingAssessment{RequiresReview: true, Authorization: current}, current)
	if decision, _, err := reviewer.Review(context.Background(), "thread-serving-review", "turn-serving-review", "call-serving", state.Action{Type: "command", Command: "ls", CWD: "/tmp"}); err != nil || decision != state.DecisionApproved || *calls != 1 {
		t.Fatalf("elevated review = %v err=%v calls=%d, want one sampled review", decision, err, *calls)
	}

	// Incomplete or changed authorization requires a fresh review.
	for _, stale := range []state.ServingAuthorization{
		{RetainedContextComplete: false, PolicyFingerprint: "model|policy"},
		{RetainedContextComplete: true, PolicyFingerprint: "other-model|policy"},
		{RetainedContextComplete: true, PolicyFingerprint: "model|policy", DeniedPaths: []string{"/secret"}},
	} {
		reviewer, calls = servingAssessmentReviewer(t, &state.ServingAssessment{Authorization: stale}, current)
		if _, _, err := reviewer.Review(context.Background(), "thread-serving-review", "turn-serving-review", "call-serving", state.Action{Type: "command", Command: "ls", CWD: "/tmp"}); err != nil || *calls != 1 {
			t.Fatalf("stale authorization review err=%v calls=%d, want a fresh review", err, *calls)
		}
	}

	// An explicit review requirement wins over a low-risk assessment.
	reviewer, calls = servingAssessmentReviewer(t, lowRisk, current)
	reviewer.requiredReview = func(string, string) bool { return true }
	if _, _, err := reviewer.Review(context.Background(), "thread-serving-review", "turn-serving-review", "call-serving", state.Action{Type: "command", Command: "ls", CWD: "/tmp"}); err != nil || *calls != 1 {
		t.Fatalf("required review err=%v calls=%d, want a fresh review", err, *calls)
	}

	// A missing assessment falls back to the synchronous review.
	reviewer, calls = servingAssessmentReviewer(t, nil, current)
	if _, _, err := reviewer.Review(context.Background(), "thread-serving-review", "turn-serving-review", "call-unscored", state.Action{Type: "command", Command: "ls", CWD: "/tmp"}); err != nil || *calls != 1 {
		t.Fatalf("missing assessment err=%v calls=%d, want a fresh review", err, *calls)
	}
}

type servingAssessmentReviewRecorder struct {
	calls int
}

func (r *servingAssessmentReviewRecorder) Review(context.Context, string, string, string, state.Action) (state.ReviewDecision, string, error) {
	r.calls++
	return state.DecisionApproved, "", nil
}

// Mirrors Rust #58075: a hook rewrite invalidates the retained serving value,
// so the rewritten call is reviewed synchronously.
func TestGuardianNativeReviewDiscardsAssessmentOnHookRewriteLikeRust(t *testing.T) {
	const threadID, turnID = "thread-serving-hook", "turn-serving-hook"
	recorder := &servingAssessmentReviewRecorder{}
	router := NewRuntimeRouter(RuntimeServices{GuardianReviewer: recorder})
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}
	router.recordGuardianServingAssessment(threadID, turnID, "action", state.ServingAssessment{
		Authorization: router.guardianServingAuthorization(threadID, turnID),
	})
	recorded := json.RawMessage(`{"type":"function_call","call_id":"action","namespace":"team","name":"send_message","arguments":"{\"target\":\"worker\"}"}`)
	invocation := &tool.Invocation{
		CallID:     "action",
		ToolName:   tool.NamespacedName("team", "send_message"),
		Payload:    tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"target":"/root"}`},
		Source:     "direct_plaintext_message",
		NativeCall: recorded,
	}
	review := router.nativeToolGuardianReview(threadID, turnID)
	if review == nil {
		t.Fatal("nativeToolGuardianReview() = nil")
	}
	if err := review(context.Background(), invocation); err != nil {
		t.Fatalf("review error = %v", err)
	}
	if recorder.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1 after a hook rewrite", recorder.calls)
	}
	if got := router.guardianServingAssessmentFor(threadID, turnID, "action"); got != nil {
		t.Fatalf("rewritten invocation kept its serving assessment: %#v", got)
	}
}
