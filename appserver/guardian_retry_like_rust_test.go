package appserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/state"
	"codex_go/telemetry"
)

// Rust #53797/#53987 (ext/guardian-reviewer/src/review.rs, retry.rs):
// a completed assessment whose captured review context changed while the
// reviewer ran is stale authorization. It is refreshed with a fresh attempt
// under the same attempt budget and deadline, and the retry is a local
// evidence refresh, so it never pays the service backoff.
func TestGuardianReviewRefreshesChangedContextLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	transcript := []string{"user: initial request"}
	var prompts []string
	calls := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			prompts = append(prompts, request.Prompt)
			if calls == 1 {
				transcript = append(transcript, "user: follow-up while reviewing")
			}
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store:   state.NewReviewStore(),
		metrics: metrics,
		transcript: func(string) []string {
			return append([]string(nil), transcript...)
		},
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-stale", "turn-stale", "call-stale", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("review error = %v", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("decision = %q, want approved", decision)
	}
	if calls != 2 {
		t.Fatalf("agent calls = %d, want 2 (one stale attempt plus one refresh)", calls)
	}
	if strings.Contains(prompts[0], "follow-up while reviewing") {
		t.Fatalf("first attempt already saw the later transcript line: %q", prompts[0])
	}
	if !strings.Contains(prompts[1], "follow-up while reviewing") {
		t.Fatalf("refresh attempt did not use the changed transcript: %q", prompts[1])
	}
	counters := guardianMetricRecords(metrics, telemetry.GuardianReviewCountMetric)
	if len(counters) != 1 {
		t.Fatalf("review counters = %d, want 1", len(counters))
	}
	if counters[0].Tags["context_changed"] != "true" {
		t.Fatalf("context_changed = %q, want true", counters[0].Tags["context_changed"])
	}
}

// Rust #53987/#47819: the stale-authorization retry yields to the executor
// instead of sleeping before the next attempt, while still honouring the
// shared attempt budget. Three changing allow attempts finish far below the
// 200ms service backoff, and exhausting the budget fails closed.
func TestGuardianReviewStaleRetrySkipsServiceBackoffLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	transcript := []string{"user: initial request"}
	calls := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			transcript = append(transcript, "user: change")
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store:   state.NewReviewStore(),
		metrics: metrics,
		transcript: func(string) []string {
			return append([]string(nil), transcript...)
		},
	}
	started := time.Now()
	decision, message, err := reviewer.Review(context.Background(), "thread-budget", "turn-budget", "call-budget", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("review error = %v", err)
	}
	if decision != state.DecisionDenied {
		t.Fatalf("decision = %q, want a failed-closed denial", decision)
	}
	if !strings.Contains(message, "authorization changed before approval could be applied") {
		t.Fatalf("message = %q, want the stale-authorization denial", message)
	}
	if calls != guardianMaxReviewAttempts {
		t.Fatalf("agent calls = %d, want %d (shared attempt budget)", calls, guardianMaxReviewAttempts)
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("stale-context retries took %v, want no service backoff", elapsed)
	}
}

// Rust #47819 invalidates only an allow: a deny is reported from the evidence
// it already saw instead of consuming the stale-authorization retry budget.
func TestGuardianReviewDenySkipsStaleRetryLikeRust(t *testing.T) {
	transcript := []string{"user: initial request"}
	calls := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			transcript = append(transcript, "user: change while reviewing")
			return &model.AgentResponse{Message: `{"riskLevel":"high","userAuthorization":"low","outcome":"deny","rationale":"risky"}`}, nil
		}),
		store: state.NewReviewStore(),
		transcript: func(string) []string {
			return append([]string(nil), transcript...)
		},
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-deny-stale", "turn-deny-stale", "call-deny-stale", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("review error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("agent calls = %d, want the deny reported without a stale retry", calls)
	}
	if decision != state.DecisionDenied {
		t.Fatalf("decision = %q, want denied", decision)
	}
}

// Rust #51683: a stale allow is rolled back to the last committed reviewer
// checkpoint before retrying. The invalidated response id must never become the
// parent of the refreshed review, while earlier valid context remains reusable.
func TestGuardianStaleAllowRollsBackReviewerHistoryLikeRust(t *testing.T) {
	transcript := []string{"user: initial request"}
	var previousIDs []string
	calls := 0
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		calls++
		previousIDs = append(previousIDs, request.PreviousResponseID)
		if calls == 1 {
			transcript = append(transcript, "user: changed while reviewing")
		}
		responseID := "fresh-review-response"
		if calls == 1 {
			responseID = "stale-review-response"
		}
		return &model.AgentResponse{Message: guardianDecisionDurationAssessment, ResponseID: responseID}, nil
	})
	session := &guardianSessionRunner{agent: agent, previous: "committed-review-response"}
	reviewer := &modelGuardianReviewer{
		agent: session,
		store: state.NewReviewStore(),
		transcript: func(string) []string {
			return append([]string(nil), transcript...)
		},
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-stale-history", "turn", "call", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("decision = %q, want approved", decision)
	}
	if calls != 2 {
		t.Fatalf("agent calls = %d, want 2", calls)
	}
	if len(previousIDs) != 2 || previousIDs[0] != "committed-review-response" || previousIDs[1] != "committed-review-response" {
		t.Fatalf("review parent ids = %#v, want the committed checkpoint for both attempts", previousIDs)
	}
	if got := session.reviewHistoryCheckpoint().previous; got != "fresh-review-response" {
		t.Fatalf("committed reviewer checkpoint = %q, want fresh-review-response", got)
	}
}

// Rust retry.rs should_retry_guardian_review: Parse failures are recoverable
// within the same attempt budget, so a later valid assessment still decides.
func TestGuardianReviewRetriesParseFailureLikeRust(t *testing.T) {
	calls := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			if calls == 1 {
				return &model.AgentResponse{Message: "not a json assessment"}, nil
			}
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store: state.NewReviewStore(),
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-parse", "turn-parse", "call-parse", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("review error = %v", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("decision = %q, want approved after the parse retry", decision)
	}
	if calls != 2 {
		t.Fatalf("agent calls = %d, want 2", calls)
	}

	// Exhausting the budget on parse failures still fails closed.
	exhaustedMetrics := state.NewTaskMetrics()
	exhaustedCalls := 0
	exhausted := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			exhaustedCalls++
			return &model.AgentResponse{Message: "not a json assessment"}, nil
		}),
		store:   state.NewReviewStore(),
		metrics: exhaustedMetrics,
	}
	decision, _, err = exhausted.Review(context.Background(), "thread-parse-2", "turn-parse-2", "call-parse-2", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("exhausted review error = %v", err)
	}
	if decision != state.DecisionDenied {
		t.Fatalf("exhausted decision = %q, want denied", decision)
	}
	if exhaustedCalls != guardianMaxReviewAttempts {
		t.Fatalf("exhausted agent calls = %d, want %d", exhaustedCalls, guardianMaxReviewAttempts)
	}
	counters := guardianMetricRecords(exhaustedMetrics, telemetry.GuardianReviewCountMetric)
	if len(counters) != 1 {
		t.Fatalf("exhausted review counters = %d, want 1", len(counters))
	}
	if counters[0].Tags["failure_reason"] != "parse_error" {
		t.Fatalf("failure_reason = %q, want parse_error", counters[0].Tags["failure_reason"])
	}
	if counters[0].Tags["context_changed"] != "false" {
		t.Fatalf("context_changed = %q, want false for parse-only retries", counters[0].Tags["context_changed"])
	}
}

// Rust metrics.rs only puts context_changed on the review counter, so an
// unchanged successful review records the false tag and the duration histogram
// stays free of it.
func TestGuardianReviewContextChangedTagOnlyOnCounterLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store:      state.NewReviewStore(),
		metrics:    metrics,
		transcript: func(string) []string { return []string{"user: stable"} },
	}
	if _, _, err := reviewer.Review(context.Background(), "thread-tag", "turn-tag", "call-tag", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()}); err != nil {
		t.Fatalf("review error = %v", err)
	}
	counters := guardianMetricRecords(metrics, telemetry.GuardianReviewCountMetric)
	if len(counters) != 1 {
		t.Fatalf("review counters = %d, want 1", len(counters))
	}
	if counters[0].Tags["context_changed"] != "false" {
		t.Fatalf("counter context_changed = %q, want false", counters[0].Tags["context_changed"])
	}
	durations := guardianMetricRecords(metrics, telemetry.GuardianReviewDurationMetric)
	if len(durations) != 1 {
		t.Fatalf("review durations = %d, want 1", len(durations))
	}
	if _, ok := durations[0].Tags["context_changed"]; ok {
		t.Fatalf("duration histogram carries context_changed: %#v", durations[0].Tags)
	}
}

// Rust retry.rs should_retry_guardian_review: the session arm retries rate
// limits, capacity failures and transient HTTP statuses, but fails closed on
// policy, budget, invalid-request and unknown errors.
func TestGuardianReviewSessionRetryClassificationLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"rate limit exceeded", &codexapi.APIError{Kind: codexapi.ErrorRateLimitExceeded, Message: "slow down"}, true},
		{"flex unavailable", &codexapi.APIError{Kind: codexapi.ErrorFlexUnavailable}, true},
		{"server overloaded", &codexapi.APIError{Kind: codexapi.ErrorServerOverloaded}, true},
		{"policy capacity unavailable", &codexapi.APIError{Kind: codexapi.ErrorPolicyCapacityUnavailable}, true},
		{"api 500", &codexapi.APIError{Kind: codexapi.ErrorAPI, Status: 500}, true},
		{"api 599", &codexapi.APIError{Kind: codexapi.ErrorAPI, Status: 599}, true},
		{"api 408", &codexapi.APIError{Kind: codexapi.ErrorAPI, Status: 408}, true},
		{"api 429", &codexapi.APIError{Kind: codexapi.ErrorAPI, Status: 429}, true},
		{"transport without status", &codexapi.APIError{Kind: codexapi.ErrorTransport}, true},
		{"stream with status", &codexapi.APIError{Kind: codexapi.ErrorStream, Status: 503}, true},
		{"api 400", &codexapi.APIError{Kind: codexapi.ErrorAPI, Status: 400}, false},
		{"api 401", &codexapi.APIError{Kind: codexapi.ErrorAPI, Status: 401}, false},
		{"context window", &codexapi.APIError{Kind: codexapi.ErrorContextWindowExceeded}, false},
		{"cyber policy", &codexapi.APIError{Kind: codexapi.ErrorCyberPolicy}, false},
		{"usage limit", &codexapi.APIError{Kind: codexapi.ErrorRateLimit}, false},
		{"plain error", errors.New("boom"), false},
	} {
		if got := guardianReviewRetryableError(testCase.err); got != testCase.retry {
			t.Fatalf("%s: retryable = %v, want %v", testCase.name, got, testCase.retry)
		}
	}

	// A recoverable session failure consumes one attempt and then succeeds.
	calls := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			if calls == 1 {
				return nil, &codexapi.APIError{Kind: codexapi.ErrorRateLimitExceeded, Message: "slow down"}
			}
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store: state.NewReviewStore(),
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-session", "turn-session", "call-session", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("review error = %v", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("decision = %q, want approved after the recoverable retry", decision)
	}
	if calls != 2 {
		t.Fatalf("agent calls = %d, want 2", calls)
	}
}

// Mirrors Rust #41215: a reviewer session that exhausts its context is rolled
// over to a fresh response chain before the retry, so the full transcript is
// replayed instead of continuing an exhausted chain.
func TestGuardianContextWindowErrorRollsOverReviewerSessionLikeRust(t *testing.T) {
	calls := 0
	var previousIDs []string
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		calls++
		previousIDs = append(previousIDs, request.PreviousResponseID)
		if calls == 1 {
			return nil, &codexapi.APIError{Kind: codexapi.ErrorContextWindowExceeded}
		}
		return &model.AgentResponse{ResponseID: "fresh-review", Message: guardianDecisionDurationAssessment}, nil
	})
	session := &guardianSessionRunner{agent: agent, previous: "exhausted-review"}
	reviewer := &modelGuardianReviewer{agent: session, store: state.NewReviewStore()}
	decision, _, err := reviewer.Review(context.Background(), "thread-rollover", "turn-rollover", "call-rollover", state.Action{
		Type: "command", Command: "ls", CWD: t.TempDir(),
	})
	if err != nil || decision != state.DecisionApproved {
		t.Fatalf("decision = %s err = %v", decision, err)
	}
	if calls != 2 || len(previousIDs) != 2 {
		t.Fatalf("calls = %d previousIDs = %#v", calls, previousIDs)
	}
	if previousIDs[0] != "exhausted-review" || previousIDs[1] != "" {
		t.Fatalf("previous response ids = %#v, want exhausted then fresh", previousIDs)
	}
}
