package agent

import (
	"errors"
	"testing"
)

func TestExecutionLimiter(t *testing.T) {
	limiter := NewExecutionLimiter(1)
	if err := limiter.EnsureCapacity(VersionV2, SourceSubAgent); err != nil {
		t.Fatalf("EnsureCapacity() error = %v", err)
	}
	guard := limiter.Guard(VersionV2, SourceSubAgent)
	if err := limiter.EnsureCapacity(VersionV2, SourceSubAgent); !errors.Is(err, ErrAgentLimitReached) {
		t.Fatalf("expected limit, got %v", err)
	}
	guard.Release()
	if err := limiter.EnsureCapacity(VersionV2, SourceSubAgent); err != nil {
		t.Fatalf("EnsureCapacity(after release) error = %v", err)
	}
}

func TestExecutionLimiterIgnoresNonSubagent(t *testing.T) {
	limiter := NewExecutionLimiter(0)
	if err := limiter.EnsureCapacity(VersionV2, SourceCli); err != nil {
		t.Fatalf("cli should not be limited: %v", err)
	}
}

func TestOpStartsTurn(t *testing.T) {
	if !OpStartsTurn(&Operation{Kind: "user_input"}) {
		t.Fatalf("user input should start turn")
	}
	if OpStartsTurn(&Operation{Kind: "inter_agent_communication"}) {
		t.Fatalf("non-triggering communication should not start turn")
	}
}

func TestResidencySlotLifecycle(t *testing.T) {
	residency := NewResidency()
	slot, ok := residency.TryReservePendingSlot(1)
	if !ok || residency.PendingSlotCount() != 1 {
		t.Fatalf("reserve = %v pending=%d", ok, residency.PendingSlotCount())
	}
	if _, ok := residency.TryReservePendingSlot(1); ok {
		t.Fatalf("second reserve should fail")
	}
	slot.Commit("thread-a")
	if residency.PendingSlotCount() != 0 || residency.ResidentCount() != 1 {
		t.Fatalf("after commit pending=%d residents=%d", residency.PendingSlotCount(), residency.ResidentCount())
	}
	candidate, ok := residency.PopLRUCandidate("")
	if !ok || candidate != "thread-a" {
		t.Fatalf("candidate = %q %v", candidate, ok)
	}
}

func TestResidencyProtectedCandidate(t *testing.T) {
	residency := NewResidency()
	residency.Touch("a")
	residency.Touch("b")
	candidate, ok := residency.PopLRUCandidate("a")
	if !ok || candidate != "b" {
		t.Fatalf("candidate = %q %v", candidate, ok)
	}
}
