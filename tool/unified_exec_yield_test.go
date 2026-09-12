package tool

import "testing"

// TestSetMaxEmptyPollYieldTimeClampsLikeRust mirrors Rust
// UnifiedExecProcessManager::new(config.background_terminal_max_timeout): the
// configured value clamps the empty-poll yield and is floored at the minimum
// empty-poll yield time.
func TestSetMaxEmptyPollYieldTimeClampsLikeRust(t *testing.T) {
	manager := NewUnifiedExecManager()
	manager.SetMaxEmptyPollYieldTime(10_000)
	if got := manager.clampWriteYield(60_000, true); got != 10_000 {
		t.Fatalf("clampWriteYield(60000, empty) = %d, want 10000", got)
	}
	// Non-empty writes keep their own cap and are unaffected by this setting.
	if got := manager.clampWriteYield(60_000, false); got != unifiedExecMaxYieldMS {
		t.Fatalf("non-empty yield = %d, want %d", got, unifiedExecMaxYieldMS)
	}

	// Values below the minimum fall back to the minimum.
	manager.SetMaxEmptyPollYieldTime(1)
	if got := manager.clampWriteYield(60_000, true); got != unifiedExecMinEmptyPollYieldMS {
		t.Fatalf("clampWriteYield with a tiny cap = %d, want %d", got, unifiedExecMinEmptyPollYieldMS)
	}

	// A larger cap allows a longer empty-poll yield.
	manager.SetMaxEmptyPollYieldTime(120_000)
	if got := manager.clampWriteYield(90_000, true); got != 90_000 {
		t.Fatalf("clampWriteYield(90000, empty) = %d, want 90000", got)
	}

	// A nil manager is a no-op.
	var nilManager *UnifiedExecManager
	nilManager.SetMaxEmptyPollYieldTime(1)
}
