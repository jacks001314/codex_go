package appserver

import (
	"context"
	"errors"
	"sync"
	"testing"

	"codex_go/model"
	"codex_go/runtimeutil"
	"codex_go/session"
	"codex_go/turn"
)

type budgetingTurnAgent struct {
	mu    sync.Mutex
	calls int
	usage model.AgentUsage
}

func (a *budgetingTurnAgent) Run(_ context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	return &model.AgentResponse{
		ResponseID: "resp-turn-budget",
		Message:    "completed answer",
		Items:      []model.AgentItem{{ID: "msg-turn-budget", Type: "agent_message", Text: "completed answer"}},
		Usage:      a.usage,
	}, nil
}

// scriptedUsageAccounting hands out prepared decisions one report at a time and
// keeps every report so a test can prove the reported bill.
type scriptedUsageAccounting struct {
	mu        sync.Mutex
	decisions []runtimeutil.UsageAccountingDecision
	reports   []runtimeutil.AgentUsageReport
}

func (a *scriptedUsageAccounting) ReportUsage(report runtimeutil.AgentUsageReport) (runtimeutil.UsageAccountingDecision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reports = append(a.reports, report)
	if len(a.decisions) == 0 {
		return runtimeutil.UsageAccountingContinue, nil
	}
	decision := a.decisions[0]
	a.decisions = a.decisions[1:]
	return decision, nil
}

// Rust #61958: a host's ExhaustedForTurn decision fails the reporting turn with
// sessionBudgetExceeded after its output and usage were recorded, while the
// next turn of the same thread stays eligible.
func TestUsageAccountingExhaustedForTurnFailsOnlyThatTurnLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	accounting := &scriptedUsageAccounting{decisions: []runtimeutil.UsageAccountingDecision{
		runtimeutil.UsageAccountingExhaustedForTurn,
	}}
	agent := &budgetingTurnAgent{usage: model.AgentUsage{InputTokens: 5, OutputTokens: 7, TotalTokens: 12}}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:    NewRouter(store),
		Turns:           turn.NewTurnService(),
		ThreadStatus:    NewThreadStatusManager(),
		Agent:           agent,
		UsageAccounting: accounting,
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID

	first := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "first turn",
	}))
	if first.Error != nil {
		t.Fatalf("first turn start error: %+v", first.Error)
	}
	firstTurnID := first.Result.(*turn.TurnStartResponse).Turn.ID
	firstCompleted := waitForTurnCompletedStatus(t, sink, firstTurnID, TurnStatusFailed)
	if firstCompleted.Turn.Error == nil || firstCompleted.Turn.Error.CodexErrorInfo != "sessionBudgetExceeded" {
		t.Fatalf("first turn error = %#v, want sessionBudgetExceeded", firstCompleted.Turn.Error)
	}

	// The completed response's output and usage survive the accounting failure.
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	found := false
	for _, item := range record.Items {
		if item.Text == "completed answer" {
			found = true
		}
	}
	if !found {
		t.Fatalf("thread items = %#v, want the completed answer preserved", record.Items)
	}
	accounting.mu.Lock()
	reports := append([]runtimeutil.AgentUsageReport(nil), accounting.reports...)
	accounting.mu.Unlock()
	if len(reports) != 1 {
		t.Fatalf("usage reports = %d, want 1", len(reports))
	}
	if reports[0].TurnID != firstTurnID || reports[0].Usage == nil || reports[0].Usage.OutputTokens != 7 {
		t.Fatalf("usage report = %#v, want the first turn's own usage", reports[0])
	}

	// Later turns are not poisoned by a turn-scoped exhaustion.
	second := router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "second turn",
	}))
	if second.Error != nil {
		t.Fatalf("second turn start error: %+v", second.Error)
	}
	secondTurnID := second.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, secondTurnID, TurnStatusCompleted)
	agent.mu.Lock()
	calls := agent.calls
	agent.mu.Unlock()
	if calls != 2 {
		t.Fatalf("model calls = %d, want both turns to sample", calls)
	}
}

// Rust #62621: a host's Exhausted decision stops new work across the rollout
// tree, so the reporting turn fails and no later turn reaches the model.
func TestUsageAccountingExhaustedStopsNewWorkLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	accounting := &scriptedUsageAccounting{decisions: []runtimeutil.UsageAccountingDecision{
		runtimeutil.UsageAccountingExhausted,
	}}
	agent := &budgetingTurnAgent{usage: model.AgentUsage{InputTokens: 5, OutputTokens: 7, TotalTokens: 12}}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:    NewRouter(store),
		Turns:           turn.NewTurnService(),
		ThreadStatus:    NewThreadStatusManager(),
		Agent:           agent,
		UsageAccounting: accounting,
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	if router.hostBudgetExhaustedFor(threadID) {
		t.Fatal("thread started already exhausted")
	}

	first := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "first turn",
	}))
	if first.Error != nil {
		t.Fatalf("first turn start error: %+v", first.Error)
	}
	firstTurnID := first.Result.(*turn.TurnStartResponse).Turn.ID
	firstCompleted := waitForTurnCompletedStatus(t, sink, firstTurnID, TurnStatusFailed)
	if firstCompleted.Turn.Error == nil || firstCompleted.Turn.Error.CodexErrorInfo != "sessionBudgetExceeded" {
		t.Fatalf("first turn error = %#v, want sessionBudgetExceeded", firstCompleted.Turn.Error)
	}
	if !router.hostBudgetExhaustedFor(threadID) {
		t.Fatal("host exhaustion was not recorded for the rollout tree")
	}
	if err := router.rolloutBudgetCheck(threadID)(); !errors.Is(err, ErrSessionBudgetExceeded) {
		t.Fatalf("work gate error = %v, want ErrSessionBudgetExceeded", err)
	}

	// New work stays refused, so the model is never asked again.
	second := router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "second turn",
	}))
	if second.Error == nil {
		secondTurnID := second.Result.(*turn.TurnStartResponse).Turn.ID
		secondCompleted := waitForTurnCompletedStatus(t, sink, secondTurnID, TurnStatusFailed)
		if secondCompleted.Turn.Error == nil || secondCompleted.Turn.Error.CodexErrorInfo != "sessionBudgetExceeded" {
			t.Fatalf("second turn error = %#v, want sessionBudgetExceeded", secondCompleted.Turn.Error)
		}
	}
	agent.mu.Lock()
	calls := agent.calls
	agent.mu.Unlock()
	if calls != 1 {
		t.Fatalf("model calls = %d, want only the reporting turn", calls)
	}
}
