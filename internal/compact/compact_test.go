package compact

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEvaluatePolicy(t *testing.T) {
	status := Evaluate(Policy{Enabled: true, TokenLimit: 100, Scope: ScopeTotal}, 120)
	if !status.ShouldCompact || status.Reason != ReasonTokenLimit {
		t.Fatalf("expected token compaction: %+v", status)
	}
	if status.TokensUntilCompaction == nil || *status.TokensUntilCompaction != -20 {
		t.Fatalf("remaining = %+v", status.TokensUntilCompaction)
	}

	status = Evaluate(Policy{Enabled: true, TokenLimit: 80, WindowTokens: 150, PrefillTokens: 50, Scope: ScopeBodyAfterPrefix}, 120)
	if status.ShouldCompact {
		t.Fatalf("should not compact yet: %+v", status)
	}
	if status.AutoCompactScopeTokens != 70 {
		t.Fatalf("scope tokens = %d", status.AutoCompactScopeTokens)
	}

	status = Evaluate(Policy{Enabled: true, TokenLimit: 1000, WindowTokens: 100}, 101)
	if !status.NewContextWindowRequired || status.Reason != ReasonContextWindowExceeded {
		t.Fatalf("window overflow not detected: %+v", status)
	}
}

func TestWindowLifecycle(t *testing.T) {
	window := NewWindowWithIDs(WindowIDs{FirstWindowID: "first", WindowID: "current"})
	next := "next"
	window.SetIDGenerator(func() string { return next })
	if window.Number() != 0 {
		t.Fatalf("number = %d", window.Number())
	}
	if !window.ClaimTokenBudgetReminder() || window.ClaimTokenBudgetReminder() {
		t.Fatal("reminder claim should only succeed once")
	}
	window.RequestNewContextWindow()
	if !window.TakeNewContextWindowRequest() || window.TakeNewContextWindowRequest() {
		t.Fatal("request should be consumed once")
	}
	window.SetEstimatedPrefill(150)
	snapshot := window.Snapshot()
	if snapshot.PrefillInputTokens == nil || *snapshot.PrefillInputTokens != 150 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	window.EnsureServerObservedPrefill(120)
	window.SetEstimatedPrefill(90)
	snapshot = window.Snapshot()
	if snapshot.PrefillInputTokens == nil || *snapshot.PrefillInputTokens != 120 {
		t.Fatalf("server observed prefill not retained: %+v", snapshot)
	}
	number, ids := window.Advance()
	if number != 1 || ids.PreviousWindowID != "current" || ids.WindowID != "next" {
		t.Fatalf("advance = %d %+v", number, ids)
	}
	if !window.ClaimTokenBudgetReminder() {
		t.Fatal("advance should reset reminder")
	}
}

func TestBuildAndValidateRequest(t *testing.T) {
	window := NewWindowWithIDs(WindowIDs{FirstWindowID: "first", WindowID: "current"})
	request, err := BuildRequest("thread-a", "turn-a", TriggerAuto, ReasonTokenLimit, PhasePreTurn, "", nil, window)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if request.Prompt == "" || request.WindowIDs.WindowID != "current" {
		t.Fatalf("request = %+v", request)
	}
	_, err = BuildRequest("", "turn-a", TriggerAuto, ReasonTokenLimit, PhasePreTurn, "", nil, window)
	if !errors.Is(err, ErrInvalidCompaction) {
		t.Fatalf("expected invalid request, got %v", err)
	}
}

func TestBuildCompactedHistory(t *testing.T) {
	history := BuildCompactedHistory(
		[]Item{{ID: "dev", Type: "message", Role: "developer", Text: "context"}},
		[]Item{{ID: "user", Type: "message", Role: "user", Kind: "user_message", Text: "please fix"}},
		"summary",
	)
	if len(history) != 3 {
		t.Fatalf("history len = %d", len(history))
	}
	if !strings.Contains(history[2].Text, SummaryPrefix) || !strings.Contains(history[2].Text, "summary") {
		t.Fatalf("summary item = %+v", history[2])
	}
}

func TestCompactLocallyInjectsInitialContext(t *testing.T) {
	request := &Request{
		ThreadID: "thread-a",
		Trigger:  TriggerAuto,
		Reason:   ReasonTokenLimit,
		Phase:    PhaseMidTurn,
		History: []Item{
			{ID: "u1", Type: "message", Role: "user", Kind: "user_message", Text: "first"},
			{ID: "a1", Type: "message", Role: "assistant", Text: "answer"},
			{ID: "u2", Type: "message", Role: "user", Kind: "user_message", Text: "second"},
		},
	}
	result, err := CompactLocally(request, 1000, []Item{{ID: "ctx", Type: "message", Role: "developer", Text: "context"}}, true)
	if err != nil {
		t.Fatalf("compact locally: %v", err)
	}
	if !result.Succeeded() {
		t.Fatalf("result = %+v", result)
	}
	if len(result.NewHistory) != 3 {
		t.Fatalf("new history = %+v", result.NewHistory)
	}
	if result.NewHistory[0].ID != "ctx" || result.NewHistory[1].ID != "u2" {
		t.Fatalf("initial context not inserted before last user: %+v", result.NewHistory)
	}
}

func TestShouldKeepCompactedHistoryItem(t *testing.T) {
	kept := []Item{
		{Type: "message", Role: "assistant"},
		{Type: "message", Role: "user", Kind: "user_message"},
		{Type: "message", Role: "user", Kind: "hook_prompt"},
		{Type: "message", Role: "user", Kind: "compaction_summary"},
		{Type: "agent_message"},
		{Type: "context_compaction"},
	}
	for _, item := range kept {
		if !ShouldKeepCompactedHistoryItem(item) {
			t.Fatalf("should keep %+v", item)
		}
	}
	dropped := []Item{
		{Type: "message", Role: "developer"},
		{Type: "message", Role: "system"},
		{Type: "message", Role: "user", Kind: "context_update"},
		{Type: "function_call"},
		{Type: "compaction_trigger"},
	}
	for _, item := range dropped {
		if ShouldKeepCompactedHistoryItem(item) {
			t.Fatalf("should drop %+v", item)
		}
	}
}

func TestProcessRemoteHistory(t *testing.T) {
	remote := []Item{
		{ID: "dev", Type: "message", Role: "developer"},
		{ID: "summary", Type: "message", Role: "user", Kind: "compaction_summary", Text: "summary"},
		{ID: "call", Type: "function_call"},
	}
	processed := ProcessRemoteHistory(remote, []Item{{ID: "ctx", Type: "message", Role: "developer"}}, true)
	if len(processed) != 2 {
		t.Fatalf("processed = %+v", processed)
	}
	if processed[0].ID != "ctx" || processed[1].ID != "summary" {
		t.Fatalf("unexpected order: %+v", processed)
	}
}

func TestItemTextFromContentAndData(t *testing.T) {
	item := Item{Content: []ContentPart{{Type: "input_text", Text: "first"}, {Type: "input_text", Text: "second"}}}
	if got := ItemText(&item); got != "first\nsecond" {
		t.Fatalf("ItemText(content) = %q", got)
	}
	item = Item{Data: map[string]any{"output": "done"}}
	if got := ItemText(&item); got != "done" {
		t.Fatalf("ItemText(data) = %q", got)
	}
}

func TestTrimHistoryToTokenBudgetKeepsRecentItems(t *testing.T) {
	items := []Item{
		{ID: "old", Text: "one two three"},
		{ID: "mid", Text: "four five"},
		{ID: "new", Text: "six seven"},
	}
	trimmed := TrimHistoryToTokenBudget(items, 4)
	if len(trimmed) != 2 || trimmed[0].ID != "mid" || trimmed[1].ID != "new" {
		t.Fatalf("trimmed = %+v", trimmed)
	}
}

func TestCompactLocallyHonorsHistoryTokenBudget(t *testing.T) {
	request := &Request{
		ThreadID:         "thread-a",
		Trigger:          TriggerAuto,
		Reason:           ReasonTokenLimit,
		Phase:            PhaseMidTurn,
		MaxHistoryTokens: 3,
		History: []Item{
			{ID: "old", Type: "message", Role: "user", Kind: "user_message", Text: "old words should drop"},
			{ID: "new", Type: "message", Role: "user", Kind: "user_message", Text: "keep this"},
		},
	}
	result, err := CompactLocally(request, 1000, nil, false)
	if err != nil {
		t.Fatalf("compact locally: %v", err)
	}
	if strings.Contains(result.Summary, "old words") || !strings.Contains(result.Summary, "keep this") {
		t.Fatalf("summary = %q", result.Summary)
	}
}

func TestCompactRemotelyUsesRunnerAndProcessesHistory(t *testing.T) {
	runner := remoteRunnerFunc(func(ctx context.Context, request *Request) (*Result, error) {
		return &Result{
			Status:  StatusCompleted,
			Summary: "remote summary",
			NewHistory: []Item{
				{ID: "dev", Type: "message", Role: "developer", Text: "drop"},
				{ID: "summary", Type: "message", Role: "user", Kind: "compaction_summary", Text: SummaryPrefix + "\nremote summary"},
			},
		}, nil
	})
	result, err := CompactRemotely(context.Background(), &Request{
		ThreadID: "thread-a",
		Trigger:  TriggerManual,
		Reason:   ReasonUserRequested,
		Phase:    PhaseStandaloneTurn,
		History:  []Item{{ID: "u1", Type: "message", Role: "user", Kind: "user_message", Text: "hello"}},
	}, &RemoteOptions{
		Runner:               runner,
		InitialContext:       []Item{{ID: "ctx", Type: "message", Role: "developer", Text: "context"}},
		InjectBeforeLastUser: true,
	})
	if err != nil {
		t.Fatalf("CompactRemotely() error = %v", err)
	}
	if result.Summary != "remote summary" {
		t.Fatalf("summary = %q", result.Summary)
	}
	if len(result.NewHistory) != 2 || result.NewHistory[0].ID != "ctx" || result.NewHistory[1].ID != "summary" {
		t.Fatalf("new history = %+v", result.NewHistory)
	}
}

func TestCompactRemotelyFallsBackToLocal(t *testing.T) {
	result, err := CompactRemotely(context.Background(), &Request{
		ThreadID: "thread-a",
		Trigger:  TriggerManual,
		Reason:   ReasonUserRequested,
		Phase:    PhaseStandaloneTurn,
		History:  []Item{{ID: "u1", Type: "message", Role: "user", Kind: "user_message", Text: "local words"}},
	}, &RemoteOptions{FallbackToLocal: true, MaxSummaryChars: 1000})
	if err != nil {
		t.Fatalf("CompactRemotely() error = %v", err)
	}
	if !strings.Contains(result.Summary, "local words") {
		t.Fatalf("summary = %q", result.Summary)
	}
}

type remoteRunnerFunc func(context.Context, *Request) (*Result, error)

func (f remoteRunnerFunc) Compact(ctx context.Context, request *Request) (*Result, error) {
	return f(ctx, request)
}
