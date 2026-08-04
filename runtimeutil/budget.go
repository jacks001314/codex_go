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

type BudgetConfig struct {
	LimitTokens               int64
	PrefillTokenWeight        float64
	SamplingTokenWeight       float64
	ReminderAtRemainingTokens []int64
}

type BudgetReminder struct {
	RemainingTokens int64
	ReminderIndex   int64
}

type Budget struct {
	mu         sync.Mutex
	configured bool
	config     BudgetConfig
	used       float64
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
// whether the configured budget is exhausted (also on later calls). When the
// provider includes `codex_rollout_budget_units`, those units are charged
// directly; otherwise weighted input/output token accounting is used. Invalid
// provider units are rejected as a fatal error, mirroring Rust's
// `RolloutBudget::record_usage`.
func (b *Budget) RecordUsage(usage TokenUsage) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.configured {
		return false, nil
	}
	var units float64
	if usage.CodexRolloutBudgetUnits != "" {
		value, err := usage.CodexRolloutBudgetUnits.Float64()
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return false, fmt.Errorf("%w: got %q", ErrInvalidRolloutBudgetUnits, usage.CodexRolloutBudgetUnits)
		}
		units = value
	} else {
		units = float64(max64(usage.OutputTokens, 0))*b.config.SamplingTokenWeight + float64((&usage).NonCachedInput())*b.config.PrefillTokenWeight
	}
	b.used += units
	return b.used >= float64(b.config.LimitTokens), nil
}

func (b *Budget) PendingReminder(threadID string, windowID string) *BudgetReminder {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.configured {
		return nil
	}
	remaining := int64(float64(b.config.LimitTokens) - b.used)
	if remaining < 0 {
		remaining = 0
	}
	var index int64
	for _, threshold := range b.config.ReminderAtRemainingTokens {
		if remaining <= threshold {
			index++
		}
	}
	if delivery, ok := b.deliveries[threadID]; ok && delivery.WindowID == windowID && delivery.ReminderIndex >= index {
		return nil
	}
	return &BudgetReminder{RemainingTokens: remaining, ReminderIndex: index}
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
