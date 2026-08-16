package exec

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

func TestExecGoalToolsCreateGetUpdateComplete(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("save auth: %v", err)
	}
	threadID := "thread-goal-tools"
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	if err := store.Save(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{},
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}
	runner := NewRunner(home)
	runner.setGoalTurnContext(threadID, "turn-1")
	cfg := &config.Config{Values: map[string]any{"features": map[string]any{"goals": true}}}
	req := &Request{Exec: cli.ExecOptions{Prompt: "work"}}
	run := &agentRunConfig{Config: cfg, ThreadID: threadID, TurnID: "turn-1"}
	executors := runner.goalToolExecutorsForRequest(req, run)
	if len(executors) != 3 {
		t.Fatalf("goal executors = %d, want 3", len(executors))
	}
	create := execGoalToolExecutorByName(t, executors, tool.GoalCreateToolName)
	update := execGoalToolExecutorByName(t, executors, tool.GoalUpdateToolName)
	get := execGoalToolExecutorByName(t, executors, tool.GoalGetToolName)
	ctx := context.Background()

	output, err := create.Execute(ctx, execGoalToolInvocation(tool.GoalCreateToolName, `{"objective":"1+1","token_budget":1000}`))
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	created := execGoalToolResponseFromOutput(t, output)
	if created.Goal == nil || created.Goal.Objective != "1+1" || created.Goal.Status != string(execGoalActive) ||
		created.Goal.TokenBudget == nil || *created.Goal.TokenBudget != 1000 {
		t.Fatalf("created goal = %#v", created.Goal)
	}
	execAssertStoredGoal(t, store, threadID, "1+1", string(execGoalActive))

	output, err = get.Execute(ctx, execGoalToolInvocation(tool.GoalGetToolName, `{}`))
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	got := execGoalToolResponseFromOutput(t, output)
	if got.Goal == nil || got.Goal.Objective != "1+1" {
		t.Fatalf("get goal = %#v", got.Goal)
	}

	output, err = update.Execute(ctx, execGoalToolInvocation(tool.GoalUpdateToolName, `{"status":"complete"}`))
	if err != nil {
		t.Fatalf("update goal: %v", err)
	}
	updated := execGoalToolResponseFromOutput(t, output)
	if updated.Goal == nil || updated.Goal.Status != string(execGoalComplete) {
		t.Fatalf("updated goal = %#v", updated.Goal)
	}
	if updated.CompletionBudgetReport == nil {
		t.Fatalf("budgeted completion should include a budget report")
	}
	execAssertStoredGoal(t, store, threadID, "1+1", string(execGoalComplete))

	// A completed goal can be replaced with a new objective.
	output, err = create.Execute(ctx, execGoalToolInvocation(tool.GoalCreateToolName, `{"objective":"next task"}`))
	if err != nil {
		t.Fatalf("create replacement goal: %v", err)
	}
	replaced := execGoalToolResponseFromOutput(t, output)
	if replaced.Goal == nil || replaced.Goal.Objective != "next task" || replaced.Goal.Status != string(execGoalActive) {
		t.Fatalf("replacement goal = %#v", replaced.Goal)
	}

	// An unfinished goal blocks creation.
	_, err = create.Execute(ctx, execGoalToolInvocation(tool.GoalCreateToolName, `{"objective":"duplicate"}`))
	if err == nil {
		t.Fatal("create while unfinished goal exists should fail")
	}

	// Review and sub-agent turns must not expose goal tools.
	reviewRun := &agentRunConfig{Config: cfg, ThreadID: threadID, TurnID: "turn-review"}
	reviewReq := &Request{Exec: cli.ExecOptions{Prompt: "review", Subcommand: "review"}}
	if tools := runner.goalToolExecutorsForRequest(reviewReq, reviewRun); len(tools) != 0 {
		t.Fatalf("review goal executors = %d, want 0", len(tools))
	}
	subagentReq := &Request{Exec: cli.ExecOptions{Prompt: "work"}, subagent: &execSubagentContext{}}
	if tools := runner.goalToolExecutorsForRequest(subagentReq, run); len(tools) != 0 {
		t.Fatalf("sub-agent goal executors = %d, want 0", len(tools))
	}
}

func execGoalToolExecutorByName(t *testing.T, executors []tool.Executor, name string) tool.Executor {
	t.Helper()
	for _, executor := range executors {
		if executor != nil && executor.Spec().Name.Name == name {
			return executor
		}
	}
	t.Fatalf("goal tool %q not found in %#v", name, executors)
	return nil
}

func execGoalToolInvocation(name string, arguments string) *tool.Invocation {
	return &tool.Invocation{
		CallID:   "call-" + name,
		ToolName: tool.PlainName(name),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: arguments},
	}
}

func execGoalToolResponseFromOutput(t *testing.T, output *tool.Output) execGoalToolResponse {
	t.Helper()
	if output == nil || output.Data == nil {
		t.Fatalf("goal tool output = %#v", output)
	}
	response, ok := output.Data["output"].(execGoalToolResponse)
	if !ok {
		t.Fatalf("goal tool output value = %#v", output.Data["output"])
	}
	return response
}

// TestExecGoalToolOutputIsStringOnWire guards against Responses API providers
// (such as DeepSeek) rejecting structured function_call_output.output objects.
func TestExecGoalToolOutputIsStringOnWire(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("save auth: %v", err)
	}
	threadID := "thread-goal-wire"
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	if err := store.Save(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{},
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}
	runner := NewRunner(home)
	runner.setGoalTurnContext(threadID, "turn-1")
	cfg := &config.Config{Values: map[string]any{"features": map[string]any{"goals": true}}}
	req := &Request{Exec: cli.ExecOptions{Prompt: "work"}}
	run := &agentRunConfig{Config: cfg, ThreadID: threadID, TurnID: "turn-1"}
	executors := runner.goalToolExecutorsForRequest(req, run)
	if len(executors) != 3 {
		t.Fatalf("goal executors = %d, want 3", len(executors))
	}
	create := execGoalToolExecutorByName(t, executors, tool.GoalCreateToolName)
	if _, err := create.Execute(context.Background(), execGoalToolInvocation(tool.GoalCreateToolName, `{"objective":"1+2"}`)); err != nil {
		t.Fatal(err)
	}
	get := execGoalToolExecutorByName(t, executors, tool.GoalGetToolName)
	output, err := get.Execute(context.Background(), execGoalToolInvocation(tool.GoalGetToolName, "{}"))
	if err != nil {
		t.Fatal(err)
	}
	if output.Body == "" {
		t.Fatalf("goal tool output body must be non-empty text, got %q", output.Body)
	}
	if _, ok := output.Data["output"].(execGoalToolResponse); !ok {
		t.Fatalf("goal tool Data output lost structured value: %#v", output.Data["output"])
	}
	response := turn.ToolResponseFromOutput(execGoalToolInvocation(tool.GoalGetToolName, "{}"), output)
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
	if !strings.Contains(wireOutput, `"objective"`) {
		t.Fatalf("wire output missing structured goal: %s", wireOutput)
	}
}

func execAssertStoredGoal(t *testing.T, store *session.Store, threadID, objective, status string) {
	t.Helper()
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		t.Fatalf("read stored record: %v", err)
	}
	raw, ok := record.Metadata.Extra[execThreadGoalExtraKey]
	if !ok {
		t.Fatalf("stored record missing thread_goal extra")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal stored goal: %v", err)
	}
	var goal execGoal
	if err := json.Unmarshal(data, &goal); err != nil {
		t.Fatalf("unmarshal stored goal: %v", err)
	}
	if goal.Objective != objective || goal.Status != status {
		t.Fatalf("stored goal = %#v, want objective=%q status=%q", goal, objective, status)
	}
}
