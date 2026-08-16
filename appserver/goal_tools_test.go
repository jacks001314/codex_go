package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

func TestGoalToolExecutorsCreateCompleteAndReplaceGoal(t *testing.T) {
	ctx := context.Background()
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	executors := router.goalToolExecutorsForTurn(&config.Config{Values: map[string]any{
		"features": map[string]any{"goals": true},
	}}, threadID, "turn-1")
	create := goalToolExecutorByName(t, executors, tool.GoalCreateToolName)
	update := goalToolExecutorByName(t, executors, tool.GoalUpdateToolName)

	output, err := create.Execute(ctx, goalToolInvocation(tool.GoalCreateToolName, `{"objective":"ship goal tools","token_budget":123}`))
	if err != nil {
		t.Fatal(err)
	}
	created := goalToolResponseFromOutput(t, output)
	if created.Goal == nil || created.Goal.Objective != "ship goal tools" || created.Goal.Status != GoalActive || created.Goal.TokenBudget == nil || *created.Goal.TokenBudget != 123 || created.RemainingTokens == nil || *created.RemainingTokens != 123 {
		t.Fatalf("created goal = %#v", created)
	}
	goal, err := stateRuntime.GetThreadGoal(ctx, threadID)
	if err != nil || goal == nil || goal.Objective != "ship goal tools" || goal.Status != state.ThreadGoalActive || goal.TokenBudget == nil || *goal.TokenBudget != 123 {
		t.Fatalf("persisted created goal = %#v, %v", goal, err)
	}

	_, err = create.Execute(ctx, goalToolInvocation(tool.GoalCreateToolName, `{"objective":"duplicate goal"}`))
	if err == nil || !strings.Contains(err.Error(), "unfinished goal") {
		t.Fatalf("duplicate create error = %v", err)
	}

	output, err = update.Execute(ctx, goalToolInvocation(tool.GoalUpdateToolName, `{"status":"complete"}`))
	if err != nil {
		t.Fatal(err)
	}
	completed := goalToolResponseFromOutput(t, output)
	if completed.Goal == nil || completed.Goal.Status != GoalComplete || completed.CompletionBudgetReport == nil {
		t.Fatalf("completed goal = %#v", completed)
	}
	goal, err = stateRuntime.GetThreadGoal(ctx, threadID)
	if err != nil || goal == nil || goal.Status != state.ThreadGoalComplete {
		t.Fatalf("persisted completed goal = %#v, %v", goal, err)
	}

	output, err = create.Execute(ctx, goalToolInvocation(tool.GoalCreateToolName, `{"objective":"replacement goal"}`))
	if err != nil {
		t.Fatal(err)
	}
	replacement := goalToolResponseFromOutput(t, output)
	if replacement.Goal == nil || replacement.Goal.Objective != "replacement goal" || replacement.Goal.Status != GoalActive {
		t.Fatalf("replacement goal = %#v", replacement)
	}
}

func TestGoalToolExecutorsHiddenForReviewAndDisabledFeature(t *testing.T) {
	router, _, threadID := newGoalToolTestRouter(t)
	if executors := router.goalToolExecutorsForTurn(&config.Config{Values: map[string]any{
		"features": map[string]any{"goals": false},
	}}, threadID, "turn-1"); len(executors) != 0 {
		t.Fatalf("disabled feature goal tools = %#v", executors)
	}

	record, err := router.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil {
		t.Fatal(err)
	}
	record.Metadata.ThreadSource = string(ThreadSourceKindSubAgentReview)
	if err := router.runtimeSaveThreadRecord(record); err != nil {
		t.Fatal(err)
	}
	if executors := router.goalToolExecutorsForTurn(&config.Config{Values: map[string]any{
		"features": map[string]any{"goals": true},
	}}, threadID, "turn-1"); len(executors) != 0 {
		t.Fatalf("review goal tools = %#v", executors)
	}
}

// TestGoalToolOutputIsStringOnWire guards against DeepSeek's Responses API
// rejecting structured function_call_output.output objects ("output should be
// a string or a list of output content parts"). The structured goal snapshot
// stays in Data for internal consumers, while the wire output must be a string.
func TestGoalToolOutputIsStringOnWire(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	if _, err := stateRuntime.ReplaceThreadGoal(context.Background(), threadID, "1+2", state.ThreadGoalActive, nil); err != nil {
		t.Fatal(err)
	}
	executors := router.goalToolExecutorsForTurn(&config.Config{Values: map[string]any{
		"features": map[string]any{"goals": true},
	}}, threadID, "turn-1")
	get := goalToolExecutorByName(t, executors, tool.GoalGetToolName)

	output, err := get.Execute(context.Background(), goalToolInvocation(tool.GoalGetToolName, "{}"))
	if err != nil {
		t.Fatal(err)
	}
	if output.Body == "" {
		t.Fatalf("goal tool output body must be non-empty text, got %q", output.Body)
	}
	if _, ok := output.Data["output"].(goalToolResponse); !ok {
		t.Fatalf("goal tool Data output lost structured value: %#v", output.Data["output"])
	}
	response := turn.ToolResponseFromOutput(goalToolInvocation(tool.GoalGetToolName, "{}"), output)
	if response == nil {
		t.Fatal("tool response is nil")
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("unmarshal wire response: %v", err)
	}
	wireOutput, ok := wire["output"].(string)
	if !ok {
		t.Fatalf("function_call_output.output must be a string on the wire, got %T: %s", wire["output"], encoded)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(wireOutput), &decoded); err != nil {
		t.Fatalf("wire output is not JSON text: %v", err)
	}
	if goal, ok := decoded["goal"].(map[string]any); !ok || goal["objective"] != "1+2" {
		t.Fatalf("wire output missing structured goal: %s", wireOutput)
	}
}

func TestGoalToolExecutorsUnavailableForLegacyMetadataPath(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter})
	if executors := router.goalToolExecutorsForTurn(&config.Config{Values: map[string]any{
		"features": map[string]any{"goals": true},
	}}, "legacy-thread", "turn-1"); len(executors) != 0 {
		t.Fatalf("legacy metadata goal tools = %#v", executors)
	}
}

func newGoalToolTestRouter(t *testing.T) (*RuntimeRouter, *state.StateRuntime, string) {
	t.Helper()
	home := t.TempDir()
	ctx := context.Background()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, sqliteConfig, "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateRuntime.Close() })

	store := session.NewStore(home)
	threadRouter := NewRouter(store)
	threadRouter.SetStateRuntime(stateRuntime)
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	threadID := "goal-tool-thread"
	record := &session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: "goal-tool-session",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           home,
			ModelProvider: "openai",
			HistoryMode:   "paginated",
		},
		Items: []session.Item{{ID: "u1", Type: "message", Role: "user", Text: "goal tool", Metadata: map[string]any{"turnId": "turn-1"}}},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := threadRouter.createThreadRollout(record, now); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, StateRuntime: stateRuntime})
	return router, stateRuntime, threadID
}

func goalToolExecutorByName(t *testing.T, executors []tool.Executor, name string) tool.Executor {
	t.Helper()
	for _, executor := range executors {
		if executor != nil && executor.Spec().Name.Name == name {
			return executor
		}
	}
	t.Fatalf("goal tool %q not found in %#v", name, executors)
	return nil
}

func goalToolInvocation(name string, arguments string) *tool.Invocation {
	return &tool.Invocation{
		CallID:   "call-" + name,
		ToolName: tool.PlainName(name),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: arguments},
	}
}

func goalToolResponseFromOutput(t *testing.T, output *tool.Output) goalToolResponse {
	t.Helper()
	if output == nil || output.Data == nil {
		t.Fatalf("goal tool output = %#v", output)
	}
	value, ok := output.Data["output"]
	if !ok {
		t.Fatalf("goal tool output data = %#v", output.Data)
	}
	response, ok := value.(goalToolResponse)
	if !ok {
		t.Fatalf("goal tool response type = %T", value)
	}
	return response
}

func TestGoalToolUpdateRejectsNonTerminalStatus(t *testing.T) {
	router, _, threadID := newGoalToolTestRouter(t)
	executors := router.goalToolExecutorsForTurn(&config.Config{Values: map[string]any{
		"features": map[string]any{"goals": true},
	}}, threadID, "turn-1")
	update := goalToolExecutorByName(t, executors, tool.GoalUpdateToolName)
	_, err := update.Execute(context.Background(), goalToolInvocation(tool.GoalUpdateToolName, `{"status":"paused"}`))
	if err == nil || !strings.Contains(err.Error(), "only mark the existing goal complete or blocked") {
		t.Fatalf("update paused error = %v", err)
	}
}

func TestGoalToolCreateAppliesMaxBudget(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	cfg := &config.Config{Values: map[string]any{
		"features": map[string]any{"goals": true},
		"goals":    map[string]any{"max_goal_token_budget": 100},
	}}
	executors := router.goalToolExecutorsForTurn(cfg, threadID, "turn-1")
	create := goalToolExecutorByName(t, executors, tool.GoalCreateToolName)

	_, err := create.Execute(context.Background(), goalToolInvocation(tool.GoalCreateToolName, `{"objective":"oversized","token_budget":101}`))
	if err == nil || !strings.Contains(err.Error(), "exceeds the maximum allowed goal token budget of 100") {
		t.Fatalf("oversized budget error = %v", err)
	}

	output, err := create.Execute(context.Background(), goalToolInvocation(tool.GoalCreateToolName, `{"objective":"default budget"}`))
	if err != nil {
		t.Fatal(err)
	}
	response := goalToolResponseFromOutput(t, output)
	if response.Goal == nil || response.Goal.TokenBudget == nil || *response.Goal.TokenBudget != 100 {
		t.Fatalf("default max budget goal = %#v", response)
	}
	goal, err := stateRuntime.GetThreadGoal(context.Background(), threadID)
	if err != nil || goal == nil || goal.TokenBudget == nil || *goal.TokenBudget != 100 {
		t.Fatalf("persisted max budget goal = %#v, %v", goal, err)
	}
}

func TestGoalToolGetAndUpdateNoGoal(t *testing.T) {
	router, _, threadID := newGoalToolTestRouter(t)
	executors := router.goalToolExecutorsForTurn(&config.Config{Values: map[string]any{
		"features": map[string]any{"goals": true},
	}}, threadID, "turn-1")
	get := goalToolExecutorByName(t, executors, tool.GoalGetToolName)
	update := goalToolExecutorByName(t, executors, tool.GoalUpdateToolName)

	output, err := get.Execute(context.Background(), goalToolInvocation(tool.GoalGetToolName, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if response := goalToolResponseFromOutput(t, output); response.Goal != nil {
		t.Fatalf("get no goal = %#v", response)
	}

	_, err = update.Execute(context.Background(), goalToolInvocation(tool.GoalUpdateToolName, `{"status":"complete"}`))
	if err == nil || !strings.Contains(err.Error(), "no goal") {
		t.Fatalf("update no goal error = %v", err)
	}
}
