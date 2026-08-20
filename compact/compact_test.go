package compact

import (
	"context"
	"encoding/json"
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

func TestEvaluatePolicyReservesFallbackBuffer(t *testing.T) {
	status := Evaluate(Policy{Enabled: true, TokenLimit: 100, WindowTokens: 500, FallbackBufferTokens: 40}, 110)
	if status.ShouldCompact || status.BaseWindowTokensRemaining == nil || *status.BaseWindowTokensRemaining != 0 || status.TokensUntilCompaction == nil || *status.TokensUntilCompaction != 30 {
		t.Fatalf("status = %+v", status)
	}
	status = Evaluate(Policy{Enabled: true, TokenLimit: 100, WindowTokens: 500, FallbackBufferTokens: 40}, 140)
	if !status.ShouldCompact || status.Reason != ReasonTokenLimit {
		t.Fatalf("buffer exhaustion status = %+v", status)
	}
}

func TestEvaluateBaseWindowRemainingUsesTighterLimitLikeRust(t *testing.T) {
	// Rust context_window_token_status reports the minimum of the auto-compact
	// scope remaining and the full context window remaining.
	status := Evaluate(Policy{Enabled: true, TokenLimit: 200, WindowTokens: 150}, 120)
	if status.BaseWindowTokensRemaining == nil || *status.BaseWindowTokensRemaining != 30 {
		t.Fatalf("window remaining should win: %+v", status)
	}
	status = Evaluate(Policy{Enabled: true, TokenLimit: 130, WindowTokens: 500}, 120)
	if status.BaseWindowTokensRemaining == nil || *status.BaseWindowTokensRemaining != 10 {
		t.Fatalf("scope remaining should win: %+v", status)
	}
	status = Evaluate(Policy{Enabled: true, TokenLimit: 130, WindowTokens: 500}, 520)
	if status.BaseWindowTokensRemaining == nil || *status.BaseWindowTokensRemaining != 0 || !status.ShouldCompact || status.Reason != ReasonContextWindowExceeded {
		t.Fatalf("window overflow status = %+v", status)
	}
}

func TestEstimateActiveContextTokensIncludesItemsAfterLastModelItem(t *testing.T) {
	items := []Item{
		{ID: "u1", Type: "message", Role: "user", Text: "first question"},
		{ID: "a1", Type: "message", Role: "assistant", Text: "an answer"},
		{ID: "u2", Type: "message", Role: "user", Text: "trailing prompt from an interrupted turn"},
	}
	if !IsModelGeneratedItem(&items[1]) {
		t.Fatalf("assistant message should be model generated")
	}
	if IsModelGeneratedItem(&items[0]) || IsModelGeneratedItem(&items[2]) {
		t.Fatalf("user messages should not be model generated")
	}
	after := ItemsAfterLastModelGeneratedItem(items)
	if len(after) != 1 || after[0].ID != "u2" {
		t.Fatalf("items after last model item = %+v", after)
	}
	active := EstimateActiveContextTokens(items, 500)
	if active <= 500 {
		t.Fatalf("active context tokens = %d, want > 500", active)
	}
	if got := EstimateActiveContextTokens(items, 0); got != EstimateTokens(after) {
		t.Fatalf("estimate without stored usage = %d, want %d", got, EstimateTokens(after))
	}
	if got := ItemsAfterLastModelGeneratedItem(nil); len(got) != 0 {
		t.Fatalf("nil items after = %+v", got)
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
	if !window.ClaimAutoCompactFallback() || window.ClaimAutoCompactFallback() {
		t.Fatal("fallback claim should only succeed once")
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

func TestEstimateTextTokensCountsCJKWithoutWhitespace(t *testing.T) {
	text := strings.Repeat("这是一个很长的中文上下文", 100)
	if got := EstimateTextTokens(text); got < 1000 {
		t.Fatalf("EstimateTextTokens() = %d, want a CJK-aware estimate", got)
	}
}

func TestEstimateTextTokensPreservesWordBasedEstimate(t *testing.T) {
	if got := EstimateTextTokens("one two three four"); got != 4 {
		t.Fatalf("EstimateTextTokens() = %d, want 4", got)
	}
}

func TestEstimateTextTokensCountsLongUnbrokenText(t *testing.T) {
	text := strings.Repeat("a", 100)
	if got := EstimateTextTokens(text); got != 25 {
		t.Fatalf("EstimateTextTokens() = %d, want 25", got)
	}
	if got := truncateTextToTokens(text, 5); EstimateTextTokens(got) > 5 || len(got) >= len(text) {
		t.Fatalf("truncateTextToTokens() kept %d bytes with estimate %d", len(got), EstimateTextTokens(got))
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
	if len(result.NewHistory) != 3 || result.NewHistory[0].ID != "ctx" || result.NewHistory[1].ID != "u1" || result.NewHistory[2].ID != "summary" {
		t.Fatalf("new history = %+v", result.NewHistory)
	}
}

func TestCompactRemotelyRetainsClientAuthoredDeveloperMessagesWhenEnabled(t *testing.T) {
	clientDeveloper := Item{
		ID:   "client-developer",
		Type: "message",
		Role: "developer",
		Text: "keep this client instruction",
		Data: map[string]any{
			"harness_metadata": map[string]any{"client_authored": true},
		},
	}
	generatedDeveloper := Item{
		ID:   "generated-developer",
		Type: "message",
		Role: "developer",
		Text: "drop generated instruction",
	}
	user := Item{
		ID:   "user",
		Type: "message",
		Role: "user",
		Kind: "user_message",
		Text: "hello",
	}
	runner := remoteRunnerFunc(func(context.Context, *Request) (*Result, error) {
		return &Result{
			Status:  StatusCompleted,
			Summary: "remote summary",
			NewHistory: []Item{
				{ID: "summary", Type: "message", Role: "user", Kind: "compaction_summary", Text: SummaryPrefix + "\nremote summary"},
			},
		}, nil
	})

	for _, enabled := range []bool{false, true} {
		result, err := CompactRemotely(context.Background(), &Request{
			ThreadID: "thread-client-developer",
			Trigger:  TriggerManual,
			Reason:   ReasonUserRequested,
			Phase:    PhaseStandaloneTurn,
			History:  []Item{clientDeveloper, generatedDeveloper, user},
		}, &RemoteOptions{
			Runner:                        runner,
			InitialContext:                []Item{{ID: "ctx", Type: "message", Role: "developer", Text: "context"}},
			InjectBeforeLastUser:          true,
			RetainClientDeveloperMessages: enabled,
		})
		if err != nil {
			t.Fatalf("CompactRemotely(enabled=%v) error = %v", enabled, err)
		}
		ids := make([]string, 0, len(result.NewHistory))
		for _, item := range result.NewHistory {
			ids = append(ids, item.ID)
		}
		want := "client-developer,ctx,user,summary"
		if !enabled {
			want = "ctx,user,summary"
		}
		if strings.Join(ids, ",") != want {
			t.Fatalf("CompactRemotely(enabled=%v) history ids = %v, want %v", enabled, ids, want)
		}
	}
}

func TestCompactRemotelyRetainsBoundedDelegatedTasksAndDropsCompletions(t *testing.T) {
	delegated := structuredAgentMessageItem(t, "task", "Message Type: NEW_TASK\nTask name: /root/worker\nSender: /root\nPayload:\n", strings.Repeat("x", 40_000))
	completion := structuredAgentMessageItem(t, "completion", "Message Type: FINAL_ANSWER\nTask name: /root\nSender: /root/worker\nPayload:\ndone", "")
	oversized := structuredAgentMessageItem(t, "oversized", "Message Type: NEW_TASK\nTask name: /root/large\nSender: /root\nPayload:\n", strings.Repeat("y", 80_000))
	runner := remoteRunnerFunc(func(context.Context, *Request) (*Result, error) {
		return &Result{Status: StatusCompleted, Summary: "remote summary", NewHistory: []Item{{ID: "summary", Type: "message", Role: "user", Kind: "compaction_summary", Text: SummaryPrefix + "\nremote summary"}}}, nil
	})
	result, err := CompactRemotely(context.Background(), &Request{
		ThreadID: "thread-agent", Trigger: TriggerManual, Reason: ReasonUserRequested, Phase: PhaseStandaloneTurn,
		History: []Item{
			{ID: "user", Type: "message", Role: "user", Kind: "user_message", Text: "delegate work"},
			completion,
			delegated,
			oversized,
		},
	}, &RemoteOptions{Runner: runner, InitialContext: []Item{{ID: "ctx", Type: "message", Role: "developer", Text: "context"}}, InjectBeforeLastUser: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(result.NewHistory))
	for _, item := range result.NewHistory {
		ids = append(ids, item.ID)
	}
	if strings.Join(ids, ",") != "user,ctx,task,summary" {
		t.Fatalf("retained history ids = %v", ids)
	}
	if string(result.NewHistory[2].Raw) != string(delegated.Raw) {
		t.Fatal("delegated task raw payload was not preserved")
	}
	if EstimateItemTokens(&delegated) > MaxRetainedAgentMessageTokens || EstimateItemTokens(&oversized) <= MaxRetainedAgentMessageTokens {
		t.Fatalf("agent token estimates delegated=%d oversized=%d", EstimateItemTokens(&delegated), EstimateItemTokens(&oversized))
	}
}

func TestEncryptedAgentMessageTokenEstimateUsesPlaintextApproximation(t *testing.T) {
	encrypted := strings.Repeat("z", 160)
	item := structuredAgentMessageItem(t, "task", "Message Type: NEW_TASK\n", encrypted)
	visibleBytes := len(item.Raw) - len(encrypted) + (len(encrypted)*9+15)/16
	want := (visibleBytes + 3) / 4
	if got := EstimateItemTokens(&item); got != want {
		t.Fatalf("EstimateItemTokens() = %d, want %d", got, want)
	}
}

func TestShouldKeepCompactedHistoryItemDropsDescendantProgressLikeRust(t *testing.T) {
	descendantProgress := Item{
		ID:   "desc-progress",
		Type: "agent_message",
		Raw:  mustAgentMessageRaw(t, "/root/child", "/root", "Message Type: MESSAGE\nTask name: /root\nSender: /root/child\nPayload:\nchild progress"),
	}
	if ShouldKeepCompactedHistoryItem(descendantProgress) {
		t.Fatal("descendant MESSAGE progress must not be retained")
	}
	rootProgress := Item{
		ID:   "root-progress",
		Type: "agent_message",
		Raw:  mustAgentMessageRaw(t, "/root", "/root/child", "Message Type: MESSAGE\nTask name: /root/child\nSender: /root\nPayload:\nparent progress"),
	}
	if !ShouldKeepCompactedHistoryItem(rootProgress) {
		t.Fatal("root-authored progress must be retained")
	}
	descendantTask := Item{
		ID:   "desc-task",
		Type: "agent_message",
		Raw:  mustAgentMessageRaw(t, "/root/child", "/root", "Message Type: NEW_TASK\nTask name: /root/worker\nSender: /root/child\nPayload:\ntask"),
	}
	if !ShouldKeepCompactedHistoryItem(descendantTask) {
		t.Fatal("descendant-authored tasks must be retained")
	}
}

func mustAgentMessageRaw(t *testing.T, author string, recipient string, text string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type":      "agent_message",
		"author":    author,
		"recipient": recipient,
		"content":   []any{map[string]any{"type": "input_text", "text": text}},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return raw
}

func structuredAgentMessageItem(t *testing.T, id string, envelope string, encrypted string) Item {
	t.Helper()
	content := []any{map[string]any{"type": "input_text", "text": envelope}}
	if encrypted != "" {
		content = append(content, map[string]any{"type": "encrypted_content", "encrypted_content": encrypted})
	}
	raw, err := json.Marshal(map[string]any{"type": "agent_message", "author": "/root", "recipient": "/root/worker", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	return Item{ID: id, Type: "agent_message", Raw: raw}
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
