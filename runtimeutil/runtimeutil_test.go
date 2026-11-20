package runtimeutil

import (
	"encoding/json"
	"errors"
	"math"
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
		LimitBudget:               100,
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
	if reminder == nil || reminder.RemainingUnits != 40 || reminder.ReminderIndex != 1 {
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

func TestBudgetAdvisoryChargesWithoutExhaustingLikeRust(t *testing.T) {
	budget := NewBudget()
	budget.Configure(BudgetConfig{
		LimitBudget:               10,
		PrefillTokenWeight:        1,
		SamplingTokenWeight:       1,
		ReminderAtRemainingTokens: []int64{5},
		Advisory:                  true,
	})
	exhausted, err := budget.RecordUsage(TokenUsage{OutputTokens: 12})
	if err != nil {
		t.Fatalf("RecordUsage error = %v", err)
	}
	if exhausted {
		t.Fatal("advisory budget reported exhaustion")
	}
	reminder := budget.PendingReminder("thread", "window")
	if reminder == nil || reminder.RemainingUnits != 0 {
		t.Fatalf("advisory reminder = %+v, want zero remaining", reminder)
	}
	exhausted, err = budget.RecordUsage(TokenUsage{OutputTokens: 100})
	if err != nil {
		t.Fatalf("RecordUsage error = %v", err)
	}
	if exhausted {
		t.Fatal("advisory budget reported exhaustion on a later call")
	}
}

func TestBudgetRecordUsagePrefersProviderRolloutBudgetUnits(t *testing.T) {
	budget := NewBudget()
	budget.Configure(BudgetConfig{
		LimitBudget:         100,
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
	// Spending exactly the allowance is allowed (Rust #61579).
	exhausted, err = budget.RecordUsage(TokenUsage{CodexRolloutBudgetUnits: json.Number("97.5")})
	if err != nil {
		t.Fatalf("RecordUsage error = %v", err)
	}
	if exhausted {
		t.Fatal("exhausted = true after exactly 100 units, want false")
	}
	// Any further charge exhausts the shared allowance.
	exhausted, err = budget.RecordUsage(TokenUsage{CodexRolloutBudgetUnits: json.Number("0.1")})
	if err != nil {
		t.Fatalf("RecordUsage error = %v", err)
	}
	if !exhausted {
		t.Fatal("exhausted = false after 100.1 total units, want true")
	}
}

func TestBudgetRecordUsageRejectsInvalidProviderUnits(t *testing.T) {
	budget := NewBudget()
	budget.Configure(BudgetConfig{LimitBudget: 100, PrefillTokenWeight: 1, SamplingTokenWeight: 1})
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

func TestBudgetRecordHookChargeAddsToProviderUnitsLikeRust(t *testing.T) {
	budget := NewBudget()
	budget.Configure(BudgetConfig{LimitBudget: 100, PrefillTokenWeight: 1, SamplingTokenWeight: 1})
	// 98 provider units + a successful 3-unit post hook cross the limit; the
	// two numbers stay separate but share one account (Rust #61580).
	exhausted, err := budget.RecordHookCharge(HookCharge{Units: 3})
	if err != nil {
		t.Fatalf("RecordHookCharge error = %v", err)
	}
	if exhausted {
		t.Fatal("hook-only 3 units against 100 exhausted the budget")
	}
	exhausted, err = budget.RecordUsage(TokenUsage{CodexRolloutBudgetUnits: json.Number("98")})
	if err != nil {
		t.Fatalf("RecordUsage error = %v", err)
	}
	if !exhausted {
		t.Fatal("98 + 3 units against 100 did not exhaust the budget")
	}
	if !budget.Exhausted() {
		t.Fatal("budget work gate not closed after exhaustion")
	}
}

func TestBudgetRecordHookChargeCombinesInOneRecordLikeRust(t *testing.T) {
	budget := NewBudget()
	budget.Configure(BudgetConfig{LimitBudget: 10, PrefillTokenWeight: 1, SamplingTokenWeight: 1})
	exhausted, err := budget.RecordUsageWithHookCharge(
		&TokenUsage{CodexRolloutBudgetUnits: json.Number("8")},
		HookCharge{Units: 3},
	)
	if err != nil {
		t.Fatalf("RecordUsageWithHookCharge error = %v", err)
	}
	if !exhausted {
		t.Fatal("8 provider + 3 hook units against 10 did not exhaust the budget")
	}
}

func TestBudgetRecordHookChargeOverflowRejectsBeforePostingLikeRust(t *testing.T) {
	budget := NewBudget()
	budget.Configure(BudgetConfig{LimitBudget: math.MaxFloat64, PrefillTokenWeight: 1, SamplingTokenWeight: 1})
	exhausted, err := budget.RecordHookCharge(HookCharge{Overflow: true})
	if !errors.Is(err, ErrRolloutHookChargeOverflow) {
		t.Fatalf("RecordHookCharge error = %v, want ErrRolloutHookChargeOverflow", err)
	}
	if exhausted {
		t.Fatal("an unrepresentable receipt reported exhaustion")
	}
	// Enforcement-enabled budgets still decline new work, but the refusal
	// left the posted balance untouched.
	if !budget.Exhausted() {
		t.Fatal("overflow did not close the work gate")
	}
	exhausted, err = budget.RecordUsage(TokenUsage{CodexRolloutBudgetUnits: json.Number("1")})
	if err != nil || exhausted {
		t.Fatalf("provider charge after overflow: exhausted=%v err=%v", exhausted, err)
	}
}

func TestBudgetRecordUsageOverflowPreservesPostedBalanceLikeRust(t *testing.T) {
	budget := NewBudget()
	budget.Configure(BudgetConfig{LimitBudget: math.MaxFloat64, PrefillTokenWeight: 1, SamplingTokenWeight: 1})
	if _, err := budget.RecordUsage(TokenUsage{CodexRolloutBudgetUnits: json.Number("1e308")}); err != nil {
		t.Fatalf("first charge error = %v", err)
	}
	// The cumulative balance would overflow, so the staged bill is dropped.
	_, err := budget.RecordUsage(TokenUsage{CodexRolloutBudgetUnits: json.Number("1e308")})
	if !errors.Is(err, ErrRolloutBudgetUsedOverflow) {
		t.Fatalf("second charge error = %v, want ErrRolloutBudgetUsedOverflow", err)
	}
	budget.mu.Lock()
	posted := budget.posted.Total()
	budget.mu.Unlock()
	if posted != 1e308 {
		t.Fatalf("posted balance = %v, want the preserved 1e308", posted)
	}
}

func TestBudgetRecordUsageOverflowLeavesAdvisoryBudgetOpen(t *testing.T) {
	budget := NewBudget()
	budget.Configure(BudgetConfig{Advisory: true, LimitBudget: 10, PrefillTokenWeight: 1, SamplingTokenWeight: 1})
	_, err := budget.RecordUsageWithHookCharge(
		&TokenUsage{CodexRolloutBudgetUnits: json.Number("1e308")},
		HookCharge{Units: 1e308},
	)
	if !errors.Is(err, ErrRolloutChargeAmountOverflow) {
		t.Fatalf("charge error = %v, want ErrRolloutChargeAmountOverflow", err)
	}
	if budget.Exhausted() {
		t.Fatal("an advisory budget stopped new work")
	}
}

func TestBudgetRecordHookChargeRejectsInvalidUnits(t *testing.T) {
	budget := NewBudget()
	budget.Configure(BudgetConfig{LimitBudget: 10, PrefillTokenWeight: 1, SamplingTokenWeight: 1})
	for _, units := range []float64{-1, math.NaN(), math.Inf(1)} {
		if _, err := budget.RecordHookCharge(HookCharge{Units: units}); !errors.Is(err, ErrInvalidRolloutBudgetCharge) {
			t.Fatalf("units %v error = %v, want ErrInvalidRolloutBudgetCharge", units, err)
		}
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
