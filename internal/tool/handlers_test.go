package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/internal/compact"
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
	_, err = handler.Execute(context.Background(), &Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"plan":[{"step":"a","status":"in_progress"},{"step":"b","status":"in_progress"}]}`},
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestRequestUserInputHandlerNormalizesAndResponds(t *testing.T) {
	handler := NewRequestUserInputHandler(func(ctx context.Context, args *RequestUserInputArgs) (*UserInputResponse, error) {
		if args.Questions[0].Header != "very-long-he" {
			t.Fatalf("header not truncated: %#v", args.Questions[0])
		}
		return &UserInputResponse{
			Answers:           map[string]string{"q": "yes"},
			StructuredAnswers: map[string][]string{"q": []string{"yes", "user_note: because"}},
		}, nil
	})
	output, err := handler.Execute(context.Background(), &Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"questions":[{"header":"very-long-header","id":"q","question":"Continue?","options":[{"label":"Yes"},{"label":"No"},{"label":"Later"},{"label":"Extra"}]}],"autoResolutionMs":60000}`},
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
