package appserver

import (
	"testing"

	"codex_go/state"
)

// TestThreadGuardianReviewAssessesRecordedPermissionRequestLikeRust covers
// Rust #58903: the final recorded request_permissions call is reviewed with the
// live permission-request action, without granting anything.
func TestThreadGuardianReviewAssessesRecordedPermissionRequestLikeRust(t *testing.T) {
	reviewer := &assessmentGuardianReviewer{
		decision: state.DecisionApproved,
		assessment: &state.Assessment{
			Outcome:           state.OutcomeAllow,
			RiskLevel:         state.RiskMedium,
			UserAuthorization: state.AuthorizationHigh,
			Rationale:         "The user asked for network access.",
		},
	}
	router := guardianReviewTestRouter(t, reviewer,
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"Network is needed."}]}`,
		`{"type":"function_call","name":"request_permissions","call_id":"grant-direct","arguments":"{\"reason\":\"need network\",\"permissions\":{\"network\":{\"enabled\":true}}}"}`,
	)
	response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
	if response.Error != nil {
		t.Fatalf("thread/guardianReview error = %+v", response.Error)
	}
	if got := guardianReviewResponseJSON(t, response); got != `{"callId":"grant-direct","assessment":{"outcome":"allow","riskLevel":"medium","userAuthorization":"high","rationale":"The user asked for network access."}}` {
		t.Fatalf("response = %s", got)
	}
	called, action, target, ctx := reviewer.snapshot()
	if !called || target != "grant-direct" {
		t.Fatalf("reviewer was not asked to assess the permission request: called=%v target=%q", called, target)
	}
	if action.Type != "request_permissions" || action.Reason != "need network" {
		t.Fatalf("action = %#v, want the recorded permission request", action)
	}
	network, ok := action.Permissions["network"].(map[string]any)
	if !ok || network["enabled"] != true {
		t.Fatalf("permissions = %#v", action.Permissions)
	}
	if got := guardianOmittedCallIDFromContext(ctx); got != "grant-direct" {
		t.Fatalf("omitted call id = %q, want grant-direct", got)
	}
}

// TestThreadGuardianReviewRejectsMalformedPermissionRequestLikeRust keeps an
// unusable recorded grant from reaching the reviewer.
func TestThreadGuardianReviewRejectsMalformedPermissionRequestLikeRust(t *testing.T) {
	reviewer := &assessmentGuardianReviewer{decision: state.DecisionApproved}
	router := guardianReviewTestRouter(t, reviewer,
		`{"type":"function_call","name":"request_permissions","call_id":"grant-empty","arguments":"{\"reason\":\"none\"}"}`,
	)
	response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("response = %+v, want invalid params", response)
	}
	if called, _, _, _ := reviewer.snapshot(); called {
		t.Fatal("a malformed permission request must not reach the reviewer")
	}
}
