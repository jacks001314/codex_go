package runtimeutil

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestResolveTimeProvider(t *testing.T) {
	provider, err := ResolveTimeProvider(TimeSourceSystem, nil)
	if err != nil {
		t.Fatalf("ResolveTimeProvider(system) error = %v", err)
	}
	if _, ok := provider.(*SystemTimeProvider); !ok {
		t.Fatalf("provider = %T, want SystemTimeProvider", provider)
	}
	_, err = ResolveTimeProvider(TimeSourceExternal, nil)
	if !errors.Is(err, ErrExternalTimeProviderRequired) {
		t.Fatalf("ResolveTimeProvider(external nil) error = %v", err)
	}
	static := &StaticTimeProvider{Now: time.Unix(100, 0)}
	provider, err = ResolveTimeProvider(TimeSourceExternal, static)
	if err != nil || provider != static {
		t.Fatalf("ResolveTimeProvider(external) = %T, %v", provider, err)
	}
}

func TestSessionMessages(t *testing.T) {
	status := &AgentStatus{Type: AgentCompleted, Message: "done"}
	message := FormatInterAgentCompletionMessage("task", "agent/a", status)
	if message == nil || !strings.Contains(*message, "done") {
		t.Fatalf("completion message = %v", message)
	}
	running := FormatInterAgentCompletionMessage("task", "agent/a", &AgentStatus{Type: AgentRunning})
	if running != nil {
		t.Fatalf("running message = %v, want nil", running)
	}
	nickname := "reviewer"
	if got := FormatSubagentContextLine("agent/a", &nickname); got != "- agent/a: reviewer" {
		t.Fatalf("context line = %q", got)
	}
}

func TestBudgetUsageReminderAndRearm(t *testing.T) {
	budget := NewBudget()
	budget.Configure(BudgetConfig{
		LimitTokens:               100,
		PrefillTokenWeight:        1,
		SamplingTokenWeight:       2,
		ReminderAtRemainingTokens: []int64{50, 10},
	})
	exhausted, err := budget.RecordUsage(TokenUsage{InputTokens: 30, CachedInputTokens: 10, OutputTokens: 20})
	if err != nil {
		t.Fatalf("RecordUsage error = %v", err)
	}
	if exhausted {
		t.Fatalf("exhausted = true, want false")
	}
	reminder := budget.PendingReminder("thread", "window")
	if reminder == nil || reminder.RemainingTokens != 40 || reminder.ReminderIndex != 1 {
		t.Fatalf("reminder = %+v", reminder)
	}
	budget.MarkReminderDelivered("thread", "window", *reminder)
	if again := budget.PendingReminder("thread", "window"); again != nil {
		t.Fatalf("reminder after delivered = %+v, want nil", again)
	}
	budget.RearmReminder("thread")
	if again := budget.PendingReminder("thread", "window"); again == nil {
		t.Fatalf("reminder after rearm = nil, want reminder")
	}
	exhausted, err = budget.RecordUsage(TokenUsage{OutputTokens: 100})
	if err != nil {
		t.Fatalf("RecordUsage error = %v", err)
	}
	if !exhausted {
		t.Fatalf("exhausted = false, want true")
	}
}

func TestBudgetRecordUsagePrefersProviderRolloutBudgetUnits(t *testing.T) {
	budget := NewBudget()
	budget.Configure(BudgetConfig{
		LimitTokens:         100,
		PrefillTokenWeight:  1,
		SamplingTokenWeight: 2,
	})
	// 2.5 provider units must be charged directly instead of the weighted
	// token accounting (output 100 * 2 would otherwise exhaust immediately).
	exhausted, err := budget.RecordUsage(TokenUsage{
		InputTokens:             30,
		CachedInputTokens:       10,
		OutputTokens:            100,
		CodexRolloutBudgetUnits: json.Number("2.5"),
	})
	if err != nil {
		t.Fatalf("RecordUsage error = %v", err)
	}
	if exhausted {
		t.Fatal("exhausted = true with 2.5 units against a 100 limit, want false")
	}
	// Reaching the limit through provider units must report exhaustion.
	exhausted, err = budget.RecordUsage(TokenUsage{CodexRolloutBudgetUnits: json.Number("97.5")})
	if err != nil {
		t.Fatalf("RecordUsage error = %v", err)
	}
	if !exhausted {
		t.Fatal("exhausted = false after 100 total units, want true")
	}
}

func TestBudgetRecordUsageRejectsInvalidProviderUnits(t *testing.T) {
	budget := NewBudget()
	budget.Configure(BudgetConfig{LimitTokens: 100, PrefillTokenWeight: 1, SamplingTokenWeight: 1})
	for _, value := range []string{"-1", "NaN", "Inf", "-Inf", "not-a-number"} {
		_, err := budget.RecordUsage(TokenUsage{CodexRolloutBudgetUnits: json.Number(value)})
		if !errors.Is(err, ErrInvalidRolloutBudgetUnits) {
			t.Fatalf("units %q error = %v, want ErrInvalidRolloutBudgetUnits", value, err)
		}
	}
	// Invalid units must not mutate the running total.
	exhausted, err := budget.RecordUsage(TokenUsage{CodexRolloutBudgetUnits: json.Number("10")})
	if err != nil || exhausted {
		t.Fatalf("valid units: exhausted=%v err=%v", exhausted, err)
	}
}

func TestDiffTrackerAddUpdateDeleteAndInvalidate(t *testing.T) {
	tracker := NewDiffTracker()
	tracker.Track("local", []FileChange{{Kind: ChangeAdd, Path: "a.txt", NewContent: "new\n"}}, true)
	if !tracker.HasUnifiedDiff() || !strings.Contains(*tracker.UnifiedDiff(), "new file mode 100644") {
		t.Fatalf("add diff = %v", tracker.UnifiedDiff())
	}
	tracker.Track("local", []FileChange{{Kind: ChangeUpdate, Path: "a.txt", OldContent: "new\n", NewContent: "newer\n"}}, true)
	if !strings.Contains(*tracker.UnifiedDiff(), "+newer") {
		t.Fatalf("update diff = %s", *tracker.UnifiedDiff())
	}
	tracker.Track("local", []FileChange{{Kind: ChangeDelete, Path: "a.txt", OldContent: "newer\n"}}, true)
	if !strings.Contains(*tracker.UnifiedDiff(), "deleted file mode 100644") {
		t.Fatalf("delete diff = %s", *tracker.UnifiedDiff())
	}
	tracker.Track("local", nil, false)
	if tracker.HasUnifiedDiff() {
		t.Fatalf("HasUnifiedDiff() = true after invalidate")
	}
}
