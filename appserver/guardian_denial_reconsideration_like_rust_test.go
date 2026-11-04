package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/state"
	"codex_go/telemetry"
)

func guardianReconsiderationPlan(enabled bool, prompt string) guardianReviewPlan {
	values := map[string]any{
		"features": map[string]any{guardianDenialReconsiderationFeatureKey: enabled},
	}
	if prompt != "" {
		values["auto_review"] = map[string]any{"experimental_denial_reconsideration_prompt": prompt}
	}
	return guardianReviewPlan{Prepared: &guardianReviewerPreparedOptions{Config: &config.Config{Values: values}}}
}

func guardianReconsiderationAssessment(outcome, rationale string) string {
	return `{"riskLevel":"high","userAuthorization":"low","outcome":"` + outcome + `","rationale":"` + rationale + `"}`
}

func guardianReconsiderationRequestText(request *model.AgentRequest) string {
	if request == nil {
		return ""
	}
	encoded, _ := json.Marshal(request.InputItems)
	return request.Prompt + "\n" + string(encoded)
}

// Rust #59107: the feature is off by default; when enabled, a valid second
// assessment replaces the provisional denial while the same review session is
// reused under the original deadline.
func TestGuardianDenialReconsiderationLikeRust(t *testing.T) {
	t.Run("feature disabled keeps the denial", func(t *testing.T) {
		calls := 0
		reviewer := &modelGuardianReviewer{
			agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
				calls++
				return &model.AgentResponse{Message: guardianReconsiderationAssessment("deny", "first")}, nil
			}),
			store:      state.NewReviewStore(),
			reviewPlan: func(string, string) guardianReviewPlan { return guardianReconsiderationPlan(false, "") },
		}
		decision, _, err := reviewer.Review(context.Background(), "thread", "turn", "call", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
		if err != nil || decision != state.DecisionDenied {
			t.Fatalf("Review() = %q, err=%v, want denied", decision, err)
		}
		if calls != 1 {
			t.Fatalf("agent calls = %d, want no reconsideration", calls)
		}
	})

	t.Run("valid allow replaces the denial", func(t *testing.T) {
		var requests []*model.AgentRequest
		metrics := state.NewTaskMetrics()
		reviewer := &modelGuardianReviewer{
			agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
				clone := *request
				requests = append(requests, &clone)
				if len(requests) == 1 {
					return &model.AgentResponse{Message: guardianReconsiderationAssessment("deny", "first"), ResponseID: "first-id"}, nil
				}
				return &model.AgentResponse{Message: guardianReconsiderationAssessment("allow", "second"), ResponseID: "second-id"}, nil
			}),
			store:      state.NewReviewStore(),
			metrics:    metrics,
			reviewPlan: func(string, string) guardianReviewPlan { return guardianReconsiderationPlan(true, "") },
		}
		decision, assessment, _, err := reviewer.review(context.Background(), "thread", "turn", "call", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
		if err != nil || decision != state.DecisionApproved || assessment == nil || assessment.Rationale != "second" {
			t.Fatalf("review() = %q/%#v, err=%v, want the reconsidered allow", decision, assessment, err)
		}
		if len(requests) != 2 {
			t.Fatalf("agent calls = %d, want first review plus reconsideration", len(requests))
		}
		if requests[1].Prompt != "" || !strings.Contains(guardianReconsiderationRequestText(requests[1]), guardianDenialReconsiderationPromptText) {
			t.Fatalf("reconsideration request did not carry the scoped developer message: %#v", requests[1])
		}
		records := metrics.Records()
		found := false
		for _, record := range records {
			if record.Name == telemetry.GuardianReviewReconsiderationMetric {
				found = record.Tags["outcome"] == "allow"
			}
		}
		if !found {
			t.Fatalf("reconsideration metrics = %#v, want outcome=allow", records)
		}
	})

	t.Run("valid deny replaces the denial", func(t *testing.T) {
		calls := 0
		reviewer := &modelGuardianReviewer{
			agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
				calls++
				if calls == 1 {
					return &model.AgentResponse{Message: guardianReconsiderationAssessment("deny", "first")}, nil
				}
				return &model.AgentResponse{Message: guardianReconsiderationAssessment("deny", "second")}, nil
			}),
			store:      state.NewReviewStore(),
			reviewPlan: func(string, string) guardianReviewPlan { return guardianReconsiderationPlan(true, "") },
		}
		decision, assessment, _, err := reviewer.review(context.Background(), "thread", "turn", "call", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
		if err != nil || decision != state.DecisionDenied || assessment == nil || assessment.Rationale != "second" {
			t.Fatalf("review() = %q/%#v, err=%v, want the second denial", decision, assessment, err)
		}
		if calls != 2 {
			t.Fatalf("agent calls = %d, want 2", calls)
		}
	})

	t.Run("custom prompt is bounded and forwarded", func(t *testing.T) {
		const custom = "Reconsider using only the recorded authorization evidence."
		var requests []*model.AgentRequest
		reviewer := &modelGuardianReviewer{
			agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
				clone := *request
				requests = append(requests, &clone)
				if len(requests) == 1 {
					return &model.AgentResponse{Message: guardianReconsiderationAssessment("deny", "first")}, nil
				}
				return &model.AgentResponse{Message: guardianReconsiderationAssessment("deny", "second")}, nil
			}),
			store:      state.NewReviewStore(),
			reviewPlan: func(string, string) guardianReviewPlan { return guardianReconsiderationPlan(true, custom) },
		}
		if _, _, _, err := reviewer.review(context.Background(), "thread", "turn", "call", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		if len(requests) != 2 || !strings.Contains(guardianReconsiderationRequestText(requests[1]), custom) {
			t.Fatalf("custom reconsideration prompt missing: %#v", requests)
		}
		if strings.Contains(guardianReconsiderationRequestText(requests[1]), guardianDenialReconsiderationPromptText) {
			t.Fatalf("default reconsideration prompt leaked with the custom prompt: %#v", requests[1])
		}
	})
}

// Rust #59118: timeout keeps the completed denial (including its assessment)
// while cancellation still aborts.
func TestGuardianDenialReconsiderationTimeoutAndCancellationLikeRust(t *testing.T) {
	t.Run("timeout keeps the original denial", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		defer cancel()
		calls := 0
		reviewer := &modelGuardianReviewer{
			agent: guardianAgentFunc(func(agentCtx context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
				calls++
				if calls == 1 {
					return &model.AgentResponse{Message: guardianReconsiderationAssessment("deny", "original")}, nil
				}
				<-agentCtx.Done()
				return nil, agentCtx.Err()
			}),
			store:      state.NewReviewStore(),
			reviewPlan: func(string, string) guardianReviewPlan { return guardianReconsiderationPlan(true, "") },
		}
		decision, assessment, _, err := reviewer.review(ctx, "thread", "turn", "call", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
		if err != nil || decision != state.DecisionDenied || assessment == nil || assessment.Rationale != "original" {
			t.Fatalf("review() = %q/%#v, err=%v, want the original denial", decision, assessment, err)
		}
		if calls != 2 {
			t.Fatalf("agent calls = %d, want first review plus timed-out reconsideration", calls)
		}
	})

	t.Run("cancellation aborts", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		reviewer := &modelGuardianReviewer{
			agent: guardianAgentFunc(func(agentCtx context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
				calls++
				if calls == 1 {
					return &model.AgentResponse{Message: guardianReconsiderationAssessment("deny", "original")}, nil
				}
				cancel()
				<-agentCtx.Done()
				return nil, agentCtx.Err()
			}),
			store:      state.NewReviewStore(),
			reviewPlan: func(string, string) guardianReviewPlan { return guardianReconsiderationPlan(true, "") },
		}
		decision, _, err := reviewer.Review(ctx, "thread", "turn", "call", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
		if err != nil || decision != state.DecisionAborted {
			t.Fatalf("Review() = %q, err=%v, want aborted", decision, err)
		}
	})
}

// An invalid follow-up keeps the first denial and discards the reviewer
// session, so the next review starts from a fresh trunk rather than continuing
// the failed assessment.
func TestGuardianInvalidReconsiderationDiscardsSessionLikeRust(t *testing.T) {
	calls := 0
	var parentIDs []string
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			parentIDs = append(parentIDs, request.PreviousResponseID)
			switch calls {
			case 1:
				return &model.AgentResponse{Message: guardianReconsiderationAssessment("deny", "first"), ResponseID: "first-id"}, nil
			case 2:
				return &model.AgentResponse{Message: "not an assessment", ResponseID: "failed-id"}, nil
			default:
				return &model.AgentResponse{Message: guardianReconsiderationAssessment("allow", "fresh"), ResponseID: "fresh-id"}, nil
			}
		}),
		store:      state.NewReviewStore(),
		reviewPlan: func(string, string) guardianReviewPlan { return guardianReconsiderationPlan(true, "") },
	}
	decision, assessment, _, err := reviewer.review(context.Background(), "thread", "turn", "call-1", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	if err != nil || decision != state.DecisionDenied || assessment == nil || assessment.Rationale != "first" {
		t.Fatalf("first review = %q/%#v, err=%v, want the original denial", decision, assessment, err)
	}
	decision, _, err = reviewer.Review(context.Background(), "thread", "turn", "call-2", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	if err != nil || decision != state.DecisionApproved {
		t.Fatalf("second Review() = %q, err=%v, want the fresh approval", decision, err)
	}
	if calls != 3 {
		t.Fatalf("agent calls = %d, want 3", calls)
	}
	if parentIDs[2] != "" {
		t.Fatalf("fresh review parent = %q, want an empty response chain", parentIDs[2])
	}
	if session := reviewer.sessionFor("thread"); session == nil || session.completedReviewCount() != 1 {
		t.Fatalf("completed review count = %v, want only the fresh review", session)
	}
}
