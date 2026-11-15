package runtimeutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
)

type TokenUsage struct {
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	// CodexRolloutBudgetUnits mirrors the provider-reported
	// `response.completed.usage.codex_rollout_budget_units` value. When present,
	// it is charged directly against the shared rollout budget instead of the
	// weighted token accounting below (Rust `TokenUsage.codex_rollout_budget_units`).
	CodexRolloutBudgetUnits json.Number
}

// ErrInvalidRolloutBudgetUnits is returned when provider-reported rollout
// budget units are non-finite or negative. Mirrors Rust's fatal response
// error for `codex_rollout_budget_units`.
var ErrInvalidRolloutBudgetUnits = errors.New("response.completed usage.codex_rollout_budget_units must be finite and non-negative")

// HookCharge is the synchronous post-hook contribution of one model response
// (Rust codex_hooks::HookCharge, #61580). Overflowed reports that summing the
// individual hook prices exceeded f64 range; Units is then the largest
// individual price, a real lower bound rather than the whole bill.
type HookCharge struct {
	Units      float64
	Overflowed bool
}

// ErrInvalidRolloutBudgetCharge is returned when a charge is non-finite or
// negative. Mirrors Rust's fatal `invalid rollout budget charge`.
var ErrInvalidRolloutBudgetCharge = errors.New("invalid rollout budget charge")

func (u *TokenUsage) NonCachedInput() int64 {
	if u == nil {
		return 0
	}
	nonCached := u.InputTokens - u.CachedInputTokens
	if nonCached < 0 {
		return 0
	}
	return nonCached
}

// BudgetUnitsSum accumulates validated, nonnegative charges without dropping
// small contributions after a large one (Rust protocol::BudgetUnitsSum,
// #61579). An unrepresentable total stays infinite.
type BudgetUnitsSum struct {
	total     float64
	remainder float64
}

// Add charges units using compensated summation.
func (s *BudgetUnitsSum) Add(units float64) {
	if s == nil || units == 0 || math.IsInf(s.total, 1) {
		return
	}
	if math.IsInf(units, 1) {
		s.total = math.Inf(1)
		s.remainder = 0
		return
	}
	// Near math.MaxFloat64 the first addition can overflow before a negative
	// remainder brings it back into range, so halve both inputs for that step.
	scale := 1.0
	if math.IsInf(s.total+units, 1) {
		scale = 0.5
	}
	previous := s.total * scale
	units *= scale
	sum := previous + units
	lost := (previous - sum) + units
	if previous < units {
		lost = (units - sum) + previous
	}
	remainder := s.remainder*scale + lost
	next := math.Max(sum+remainder, previous)
	s.total = next / scale
	if math.IsInf(s.total, 1) {
		s.remainder = 0
		return
	}
	s.remainder = ((sum - next) + remainder) / scale
}

// Total reports the posted spending so far.
func (s *BudgetUnitsSum) Total() float64 {
	if s == nil {
		return 0
	}
	return s.total
}

type BudgetConfig struct {
	// LimitBudget is the positive, finite allowance in budget units. Spending
	// exactly this amount is allowed.
	LimitBudget float64
	// Advisory keeps shared accounting and budget reminders active without
	// stopping turns once the budget is exhausted (Rust
	// `features.rollout_budget.enforce_limit = false`, #57187). The zero value
	// enforces the limit.
	Advisory                  bool
	PrefillTokenWeight        float64
	SamplingTokenWeight       float64
	ReminderAtRemainingTokens []int64
	// ReminderAtContextStart restates the balance when a thread starts or its
	// context is compacted. Nil defaults to true (Rust #61579).
	ReminderAtContextStart *bool
}

type BudgetReminder struct {
	RemainingUnits float64
	ReminderIndex  int64
}

type Budget struct {
	mu         sync.Mutex
	configured bool
	exhausted  bool
	config     BudgetConfig
	posted     BudgetUnitsSum
	deliveries map[string]budgetDelivery
}

type budgetDelivery struct {
	WindowID      string
	ReminderIndex int64
}

func NewBudget() *Budget {
	return &Budget{deliveries: map[string]budgetDelivery{}}
}

func (b *Budget) Configure(config BudgetConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if config.PrefillTokenWeight == 0 {
		config.PrefillTokenWeight = 1
	}
	if config.SamplingTokenWeight == 0 {
		config.SamplingTokenWeight = 1
	}
	b.config = config
	b.configured = true
}

// RecordUsage charges usage against the shared rollout budget and reports
// whether the configured budget is exhausted (also on later calls). Advisory
// budgets keep charging but never report exhaustion. When the
// provider includes `codex_rollout_budget_units`, those units are charged
// directly; otherwise weighted input/output token accounting is used. Invalid
// provider units are rejected as a fatal error, mirroring Rust's
// `RolloutBudget::record_usage`.
func (b *Budget) RecordUsage(usage TokenUsage) (bool, error) {
	return b.RecordUsageWithHookCharge(&usage, HookCharge{})
}

// RecordUsageWithHookCharge charges one response's provider usage together
// with the synchronous post-hook contribution it settled (Rust #61580
// `RolloutBudget::record_usage`). The two numbers stay separate: provider
// units are read from `codex_rollout_budget_units` or weighted tokens, and the
// hook charge is added on top.
func (b *Budget) RecordUsageWithHookCharge(usage *TokenUsage, hook HookCharge) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.configured {
		return false, nil
	}
	if math.IsNaN(hook.Units) || math.IsInf(hook.Units, 0) || hook.Units < 0 {
		return false, ErrInvalidRolloutBudgetCharge
	}
	if hook.Overflowed {
		// All allowances are finite. No future nonnegative receipt can make
		// this total representable; the remaining allowance is known zero.
		b.posted.Add(math.Inf(1))
		if !b.config.Advisory {
			b.stopNewToolsLocked()
		}
	}
	if math.IsInf(b.posted.Total(), 1) {
		return !b.config.Advisory, nil
	}
	var units float64
	if usage != nil {
		var err error
		units, err = b.providerUnitsLocked(*usage)
		if err != nil {
			return false, err
		}
	}
	b.posted.Add(units)
	b.posted.Add(hook.Units)
	if math.IsInf(b.posted.Total(), 1) {
		if !b.config.Advisory {
			b.stopNewToolsLocked()
		}
		return false, ErrInvalidRolloutBudgetCharge
	}
	// Equality is allowed, including one rounding step (0.1 + 0.2 versus 0.3).
	exhausted := !b.config.Advisory && b.posted.Total() > math.Nextafter(b.config.LimitBudget, math.Inf(1))
	if exhausted {
		b.stopNewToolsLocked()
	}
	return exhausted, nil
}

// RecordHookCharge charges synchronous post-hook units on their own. A hook
// price is independent of the provider's usage record, so a completed
// response without token usage still pays it (Rust #61580).
func (b *Budget) RecordHookCharge(hook HookCharge) (bool, error) {
	return b.RecordUsageWithHookCharge(nil, hook)
}

func (b *Budget) providerUnitsLocked(usage TokenUsage) (float64, error) {
	if usage.CodexRolloutBudgetUnits != "" {
		value, err := usage.CodexRolloutBudgetUnits.Float64()
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return 0, fmt.Errorf("%w: got %q", ErrInvalidRolloutBudgetUnits, usage.CodexRolloutBudgetUnits)
		}
		return value, nil
	}
	return float64(max64(usage.OutputTokens, 0))*b.config.SamplingTokenWeight + float64(usage.NonCachedInput())*b.config.PrefillTokenWeight, nil
}

// stopNewToolsLocked marks the shared session budget as exhausted so new tool
// work is gated (Rust RolloutBudget::stop_new_tools).
func (b *Budget) stopNewToolsLocked() {
	b.exhausted = true
}

func (b *Budget) PendingReminder(threadID string, windowID string) *BudgetReminder {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.configured {
		return nil
	}
	remaining := math.Max(b.config.LimitBudget-b.posted.Total(), 0)
	var index int64
	for _, threshold := range b.config.ReminderAtRemainingTokens {
		if RolloutBudgetThresholdReached(remaining, threshold) {
			index++
		}
	}
	previous, hasDelivery := b.deliveries[threadID]
	previousIndex := int64(0)
	if hasDelivery {
		previousIndex = previous.ReminderIndex
	}
	crossedThreshold := index > previousIndex
	reminderAtContextStart := b.config.ReminderAtContextStart == nil || *b.config.ReminderAtContextStart
	newWindow := reminderAtContextStart && (!hasDelivery || previous.WindowID != windowID)
	if !crossedThreshold && !newWindow {
		return nil
	}
	return &BudgetReminder{RemainingUnits: remaining, ReminderIndex: index}
}

// RolloutBudgetThresholdReached reports whether the actual fractional balance
// is at or below an integer threshold. The balance is rounded up, not the
// threshold, so thresholds above 2^53 stay exact (Rust #61579).
func RolloutBudgetThresholdReached(remainingUnits float64, threshold int64) bool {
	if math.IsNaN(remainingUnits) {
		return false
	}
	rounded := math.Ceil(remainingUnits)
	if math.IsInf(rounded, 1) || rounded >= 9223372036854775808.0 {
		return false
	}
	if rounded <= -9223372036854775808.0 {
		return true
	}
	return int64(rounded) <= threshold
}

func (b *Budget) MarkReminderDelivered(threadID string, windowID string, reminder BudgetReminder) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.configured {
		return
	}
	b.deliveries[threadID] = budgetDelivery{WindowID: windowID, ReminderIndex: reminder.ReminderIndex}
}

func (b *Budget) RearmReminder(threadID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.deliveries, threadID)
}

func max64(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Exhausted reports whether the shared budget has stopped new tool work.
func (b *Budget) Exhausted() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exhausted
}
