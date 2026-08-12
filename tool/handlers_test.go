package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/compact"
)

func TestPlanHandlerUpdatesStore(t *testing.T) {
	store := NewPlanStore()
	handler := NewPlanHandler(store)
	output, err := handler.Execute(context.Background(), &Invocation{
		ToolName: PlainName("update_plan"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"plan":[{"step":"write","status":"in_progress"},{"step":"done","status":"pending"}]}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.Body != "Plan updated" {
		t.Fatalf("output = %#v", output)
	}
	if output.Data["planUpdate"] != true {
		t.Fatalf("plan output data = %#v", output.Data)
	}
	planData, ok := output.Data["plan"].([]PlanItem)
	if !ok || len(planData) != 2 || planData[0].Status != PlanInProgress {
		t.Fatalf("plan output data = %#v", output.Data["plan"])
	}
	_, plan, _ := store.Snapshot()
	if len(plan) != 2 || plan[0].Status != PlanInProgress {
		t.Fatalf("plan = %#v", plan)
	}
	// Rust's update_plan does not enforce the "one in_progress" rule at
	// runtime; parallel agent workflows may mark several steps in_progress.
	output, err = handler.Execute(context.Background(), &Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"plan":[{"step":"a","status":"in_progress"},{"step":"b","status":"in_progress"}]}`},
	})
	if err != nil || !output.Success {
		t.Fatalf("multiple in_progress plan items rejected: err=%v output=%#v", err, output)
	}
	_, plan, _ = store.Snapshot()
	if len(plan) != 2 || plan[0].Status != PlanInProgress || plan[1].Status != PlanInProgress {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestRequestUserInputHandlerNormalizesAndResponds(t *testing.T) {
	handler := NewRequestUserInputHandler(func(ctx context.Context, args *RequestUserInputArgs) (*UserInputResponse, error) {
		if args.Questions[0].Header != "very-long-he" {
			t.Fatalf("header not truncated: %#v", args.Questions[0])
		}
		if !args.Questions[0].IsOther || !args.Questions[0].IsSecret {
			t.Fatalf("question flags not preserved: %#v", args.Questions[0])
		}
		return &UserInputResponse{
			Answers:           map[string]string{"q": "yes"},
			StructuredAnswers: map[string][]string{"q": []string{"yes", "user_note: because"}},
		}, nil
	})
	output, err := handler.Execute(context.Background(), &Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"questions":[{"header":"very-long-header","id":"q","question":"Continue?","isOther":true,"isSecret":true,"options":[{"label":"Yes"},{"label":"No"},{"label":"Later"},{"label":"Extra"}]}],"autoResolutionMs":60000}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var response UserInputResponse
	if err := json.Unmarshal([]byte(output.Body), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.Answers["q"] != "yes" {
		t.Fatalf("response = %#v", response)
	}
	if got := response.StructuredAnswers["q"]; len(got) != 2 || got[0] != "yes" || got[1] != "user_note: because" {
		t.Fatalf("structured response = %#v", response.StructuredAnswers)
	}
}

func TestRequestUserInputHandlerSpecDescriptionMatchesRustModes(t *testing.T) {
	defaultSpec := NewRequestUserInputHandler(nil).Spec()
	if !strings.Contains(defaultSpec.Description, "This tool is only available in Plan mode.") {
		t.Fatalf("default description = %q", defaultSpec.Description)
	}
	if !strings.Contains(defaultSpec.Description, "Set autoResolutionMs, from 60000 to 240000 milliseconds") {
		t.Fatalf("default description missing Rust auto resolution guidance: %q", defaultSpec.Description)
	}

	featureSpec := NewRequestUserInputHandlerWithModes(nil, []string{"Default", "Plan"}).Spec()
	if !strings.Contains(featureSpec.Description, "This tool is only available in Default or Plan mode.") {
		t.Fatalf("feature description = %q", featureSpec.Description)
	}
}

func TestGetContextRemainingHandler(t *testing.T) {
	left := 42
	handler := NewGetContextRemainingHandler(func() compact.TokenStatus {
		return compact.TokenStatus{TokensUntilCompaction: &left}
	})
	output, err := handler.Execute(context.Background(), &Invocation{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output.Body, "42") {
		t.Fatalf("output = %#v", output)
	}
}

func TestNewContextWindowHandler(t *testing.T) {
	called := false
	handler := NewContextWindowHandler(func() { called = true })
	if spec := handler.Spec(); spec.Name.Key() != "new_context" || !strings.Contains(spec.Description, "new context window") {
		t.Fatalf("new_context spec = %#v", spec)
	}
	output, err := handler.Execute(context.Background(), &Invocation{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !called || !output.Success || !strings.Contains(output.Body, "new context window") {
		t.Fatalf("handler output = %#v, called = %v", output, called)
	}
}

func TestSleepHandler(t *testing.T) {
	handler := &SleepHandler{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := handler.Execute(ctx, &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: `{"duration_ms":1000}`}})
	if err == nil {
		t.Fatalf("expected cancelled sleep")
	}
	output, err := handler.Execute(context.Background(), &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: `{"duration_ms":0}`}})
	if err != nil || output.Body != "ok" {
		t.Fatalf("Execute() = %#v/%v", output, err)
	}
}

func TestRegisterCoreHandlersWithOptionsClockTools(t *testing.T) {
	now := time.Date(2026, 6, 17, 15, 20, 55, 0, time.UTC)
	clock := &fakeClockProvider{now: now}
	registry := NewRegistry()
	if err := RegisterCoreHandlersWithOptions(registry, &CoreHandlerOptions{
		ClockProvider:     clock,
		ThreadID:          "thread-clock",
		EnableCurrentTime: true,
		EnableClockSleep:  true,
	}); err != nil {
		t.Fatalf("RegisterCoreHandlersWithOptions() error = %v", err)
	}
	if _, ok := registry.Lookup(NamespacedName("clock", "curr_time")); !ok {
		t.Fatal("missing clock.curr_time")
	}
	if _, ok := registry.Lookup(NamespacedName("clock", "sleep")); !ok {
		t.Fatal("missing clock.sleep")
	}
	if _, ok := registry.Lookup(PlainName("sleep")); ok {
		t.Fatal("legacy plain sleep should not be registered for configured core handlers")
	}

	current, err := NewRouter(registry).Dispatch(context.Background(), &Invocation{
		CallID:   "time",
		ToolName: NamespacedName("clock", "curr_time"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{}`},
	})
	if err != nil {
		t.Fatalf("Dispatch current time error = %v", err)
	}
	if current.Data["current_time"] != "2026-06-17 15:20:55 UTC" || clock.threadID != "thread-clock" {
		t.Fatalf("current time output = %#v thread=%q", current, clock.threadID)
	}
	sleep, err := NewRouter(registry).Dispatch(context.Background(), &Invocation{
		CallID:   "sleep",
		ToolName: NamespacedName("clock", "sleep"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"duration_ms":10}`},
	})
	if err != nil {
		t.Fatalf("Dispatch sleep error = %v", err)
	}
	if !sleep.Success || clock.slept != 10*time.Millisecond {
		t.Fatalf("sleep output = %#v slept=%s", sleep, clock.slept)
	}
}

func TestRegisterCoreHandlers(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterCoreHandlers(registry, nil, nil, nil); err != nil {
		t.Fatalf("RegisterCoreHandlers() error = %v", err)
	}
	names := registry.Names()
	if len(names) != 4 {
		t.Fatalf("names = %#v", names)
	}
}

func TestRustCoreToolSurfaceParity(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterCoreHandlers(registry, nil, nil, nil); err != nil {
		t.Fatalf("RegisterCoreHandlers() error = %v", err)
	}
	for _, name := range []string{"update_plan", "request_user_input"} {
		if _, ok := registry.Lookup(PlainName(name)); !ok {
			t.Fatalf("missing Rust tool %q in registry", name)
		}
	}
	execSpec := NewShellExecutor(&ShellExecutorOptions{
		UnifiedExec:             NewUnifiedExecManager(),
		UnifiedExecEnvironments: []UnifiedExecEnvironment{{ID: "primary"}},
	}).Spec()
	if execSpec.Name.Key() != "exec_command" {
		t.Fatalf("exec_command spec = %#v", execSpec)
	}
	if execSpec.OutputSchema == nil || execSpec.OutputSchema["additionalProperties"] != false {
		t.Fatalf("exec_command output schema = %#v", execSpec.OutputSchema)
	}
	writeSpec := NewWriteStdinExecutor(nil, nil).Spec()
	if writeSpec.Name.Key() != "write_stdin" {
		t.Fatalf("write_stdin spec = %#v", writeSpec)
	}
	if writeSpec.OutputSchema == nil || writeSpec.OutputSchema["additionalProperties"] != false {
		t.Fatalf("write_stdin output schema = %#v", writeSpec.OutputSchema)
	}
	planSpec := NewPlanHandler(NewPlanStore()).Spec()
	if planSpec.Name.Key() != "update_plan" || planSpec.Description == "" {
		t.Fatalf("update_plan spec = %#v", planSpec)
	}
	if planSpec.InputSchema["type"] != "object" {
		t.Fatalf("update_plan schema type = %#v, want object", planSpec.InputSchema["type"])
	}
	required, _ := planSpec.InputSchema["required"].([]string)
	if len(required) != 1 || required[0] != "plan" {
		t.Fatalf("update_plan required = %#v", planSpec.InputSchema["required"])
	}
	properties, _ := planSpec.InputSchema["properties"].(map[string]any)
	plan, ok := properties["plan"].(map[string]any)
	if !ok || plan["type"] != "array" {
		t.Fatalf("update_plan plan property = %#v", plan)
	}
	items, _ := plan["items"].(map[string]any)
	status, _ := items["properties"].(map[string]any)["status"].(map[string]any)
	enum, _ := status["enum"].([]string)
	if len(enum) != 3 || enum[1] != "in_progress" {
		t.Fatalf("update_plan status enum = %#v", status)
	}
	userInputSpec := NewRequestUserInputHandler(nil).Spec()
	if !strings.Contains(userInputSpec.Description, "This tool is only available in Plan mode.") || userInputSpec.Exposure != ExposureDirectModelOnly {
		t.Fatalf("request_user_input spec = %#v", userInputSpec)
	}
}

type fakeClockProvider struct {
	now      time.Time
	threadID string
	slept    time.Duration
}

func (p *fakeClockProvider) CurrentTime(ctx context.Context, threadID string) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	p.threadID = threadID
	return p.now, nil
}

func (p *fakeClockProvider) Sleep(ctx context.Context, threadID string, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.threadID = threadID
	p.slept = duration
	return nil
}
