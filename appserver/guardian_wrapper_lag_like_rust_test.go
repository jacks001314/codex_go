package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/state"
	"codex_go/tool"
)

func TestGuardianWrapperLagDiscountsOnlyOwnUnscoredWrapperLikeRust(t *testing.T) {
	reviewer := &modelGuardianReviewer{scoreProgress: map[string]*guardianScoreProgress{}}
	threadID := "thread-wrapper-lag"
	record := func(callID, originItemID, source string, wrapper bool) {
		t.Helper()
		invocation := &tool.Invocation{
			CallID:   callID,
			ToolName: tool.PlainName("exec_command"),
			Payload:  tool.Payload{Kind: tool.PayloadFunction},
			Source:   source,
		}
		if wrapper {
			invocation.ToolName = tool.PlainName(tool.CodeModeExecToolName)
			invocation.Payload = tool.Payload{Kind: tool.PayloadCustom}
		}
		if originItemID != "" {
			invocation.Context = map[string]any{tool.OriginItemIDContextKey: originItemID}
		}
		reviewer.recordToolStart(threadID, invocation)
	}

	record("wrapper", "wrapper-item", "", true)
	record("first", "wrapper-item", "code_mode", false)
	if got := reviewer.scoreLag(threadID, "first"); got != 1 {
		t.Fatalf("own unscored wrapper lag = %d, want 1", got)
	}

	progress := reviewer.scoreProgress[threadID]
	progress.latestScoredToolCall = 1
	if got := reviewer.scoreLag(threadID, "first"); got != 1 {
		t.Fatalf("already scored wrapper lag = %d, want full lag 1", got)
	}

	record("second", "wrapper-item", "code_mode", false)
	if got := reviewer.scoreLag(threadID, "second"); got != 2 {
		t.Fatalf("nested call after scored wrapper lag = %d, want 2", got)
	}

	record("other-wrapper", "other-item", "", true)
	record("other-first", "other-item", "code_mode", false)
	if got := reviewer.scoreLag(threadID, "other-first"); got != 3 {
		t.Fatalf("other wrapper lag = %d, want 3 with only its own wrapper discounted", got)
	}

	progress.latestScoredToolCall = 4
	if got := reviewer.scoreLag(threadID, "other-first"); got != 1 {
		t.Fatalf("covered other wrapper lag = %d, want 1", got)
	}
	if got := reviewer.scoreLag(threadID, "unknown"); got != 1 {
		t.Fatalf("missing provenance lag = %d, want full lag 1", got)
	}

	for range 300 {
		record("output-only", "wrapper-item", "", false)
	}
	record("late-child", "other-item", "code_mode", false)
	if got := reviewer.scoreLag(threadID, "late-child"); got != 302 {
		t.Fatalf("evicted provenance lag = %d, want full lag 302", got)
	}
}

func TestRuntimeToolStartFeedsGuardianWrapperLagLikeRust(t *testing.T) {
	reviewer := &modelGuardianReviewer{scoreProgress: map[string]*guardianScoreProgress{}}
	router := &RuntimeRouter{services: RuntimeServices{GuardianReviewer: reviewer}}
	threadID := "thread-wrapper-hook"
	wrapper := &tool.Invocation{
		CallID:   "wrapper",
		ToolName: tool.PlainName(tool.CodeModeExecToolName),
		Payload:  tool.Payload{Kind: tool.PayloadCustom},
		Context:  map[string]any{tool.OriginItemIDContextKey: "wrapper-item"},
	}
	nested := &tool.Invocation{
		CallID:   "nested",
		ToolName: tool.PlainName("exec_command"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction},
		Source:   "code_mode",
		Context:  map[string]any{tool.OriginItemIDContextKey: "wrapper-item"},
	}
	notifier := router.runtimeToolStartedNotifier(threadID, "turn-wrapper-hook", "", false)
	notifier(context.Background(), wrapper, time.Now())
	notifier(context.Background(), nested, time.Now())
	if got := reviewer.scoreLag(threadID, "nested"); got != 1 {
		t.Fatalf("lifecycle-fed wrapper lag = %d, want 1", got)
	}
}

func TestGuardianReviewUsesOwnWrapperDiscountLikeRust(t *testing.T) {
	calls := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store:          state.NewReviewStore(),
		breaker:        state.NewCircuitBreaker(),
		scoreProgress:  map[string]*guardianScoreProgress{},
		maxToolCallLag: 1,
	}
	threadID := "thread-wrapper-review"
	record := func(callID, originItemID, source string, wrapper bool) {
		t.Helper()
		invocation := &tool.Invocation{
			CallID:   callID,
			ToolName: tool.PlainName("exec_command"),
			Payload:  tool.Payload{Kind: tool.PayloadFunction},
			Source:   source,
			Context:  map[string]any{tool.OriginItemIDContextKey: originItemID},
		}
		if wrapper {
			invocation.ToolName = tool.PlainName(tool.CodeModeExecToolName)
			invocation.Payload = tool.Payload{Kind: tool.PayloadCustom}
		}
		reviewer.recordToolStart(threadID, invocation)
	}
	review := func(callID string) (state.ReviewDecision, error) {
		decision, _, err := reviewer.Review(context.Background(), threadID, "turn-wrapper", callID, state.Action{Type: "command", Command: "ls", CWD: "/repo"})
		return decision, err
	}

	record("wrapper", "wrapper-item", "", true)
	record("first", "wrapper-item", "code_mode", false)
	decision, err := review("first")
	if err != nil || decision != state.DecisionApproved || calls != 1 {
		t.Fatalf("first review = %v %v calls=%d, want approval with its wrapper discounted", decision, err, calls)
	}

	record("second", "wrapper-item", "code_mode", false)
	decision, err = review("second")
	if err != nil || decision != state.DecisionApproved || calls != 2 {
		t.Fatalf("second review = %v %v calls=%d, want approval", decision, err, calls)
	}

	record("third", "wrapper-item", "code_mode", false)
	record("fourth", "wrapper-item", "code_mode", false)
	decision, err = review("third")
	if err != nil || decision != state.DecisionDenied || calls != 2 {
		t.Fatalf("review above lag = %v %v calls=%d, want denied without sampling", decision, err, calls)
	}
}
