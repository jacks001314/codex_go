package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/state"
)

// TestThreadGuardianReviewAssessesRecordedPatchLikeRust covers Rust #58899:
// direct review verifies a recorded native apply_patch call and hands the live
// apply_patch approval action to Guardian without applying anything.
func TestThreadGuardianReviewAssessesRecordedPatchLikeRust(t *testing.T) {
	reviewer := &assessmentGuardianReviewer{
		decision: state.DecisionApproved,
		assessment: &state.Assessment{
			Outcome:           state.OutcomeAllow,
			RiskLevel:         state.RiskMedium,
			UserAuthorization: state.AuthorizationHigh,
			Rationale:         "User requested the file.",
		},
	}
	router := guardianReviewTestRouter(t, reviewer,
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"Add a file."}]}`,
		`{"type":"custom_tool_call","name":"apply_patch","call_id":"patch-direct","status":"completed","input":"*** Begin Patch\n*** Add File: guardian-preview.txt\n+preview\n*** End Patch"}`,
	)
	response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
	if response.Error != nil {
		t.Fatalf("thread/guardianReview error = %+v", response.Error)
	}
	called, action, target, ctx := reviewer.snapshot()
	if !called || target != "patch-direct" {
		t.Fatalf("reviewer was not asked to assess the recorded patch: called=%v target=%q", called, target)
	}
	if action.Type != "apply_patch" || !strings.Contains(action.Patch, "guardian-preview.txt") {
		t.Fatalf("action = %#v, want the recorded patch", action)
	}
	if got := guardianOmittedCallIDFromContext(ctx); got != "patch-direct" {
		t.Fatalf("omitted call id = %q, want patch-direct", got)
	}
	path := filepath.Join(router.services.DefaultCWD, "guardian-preview.txt")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("direct review wrote the patch target: %v", err)
	}
}

// TestThreadGuardianReviewAssessesDeniedPatchLikeRust keeps the outside-root
// patch reviewable and still writes nothing.
func TestThreadGuardianReviewAssessesDeniedPatchLikeRust(t *testing.T) {
	reviewer := &assessmentGuardianReviewer{decision: state.DecisionApproved}
	router := guardianReviewTestRouter(t, reviewer,
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"Write outside the workspace."}]}`,
		`{"type":"custom_tool_call","name":"apply_patch","call_id":"patch-outside","status":"completed","input":"*** Begin Patch\n*** Add File: ../guardian-outside.txt\n+outside\n*** End Patch"}`,
	)
	response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
	if response.Error != nil {
		t.Fatalf("thread/guardianReview error = %+v", response.Error)
	}
	if called, action, _, _ := reviewer.snapshot(); !called || action.Type != "apply_patch" {
		t.Fatalf("denied patch action = %#v, called=%v", action, called)
	}
	outside := filepath.Join(filepath.Dir(router.services.DefaultCWD), "guardian-outside.txt")
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("direct review wrote the denied patch target: %v", err)
	}
}

// TestThreadGuardianReviewRejectsPatchOutputLikeRust keeps the final-item rule:
// a trailing custom tool output is not a reviewable recorded call.
func TestThreadGuardianReviewRejectsPatchOutputLikeRust(t *testing.T) {
	reviewer := &assessmentGuardianReviewer{decision: state.DecisionApproved}
	router := guardianReviewTestRouter(t, reviewer,
		`{"type":"custom_tool_call","name":"apply_patch","call_id":"patch-direct","status":"completed","input":"*** Begin Patch\n*** Add File: guardian-preview.txt\n+preview\n*** End Patch"}`,
		`{"type":"custom_tool_call_output","call_id":"patch-direct","output":"Done!"}`,
	)
	response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("response = %+v, want invalid params", response)
	}
	if called, _, _, _ := reviewer.snapshot(); called {
		t.Fatal("an invalid trailing item must not reach the reviewer")
	}
}
