package retainedctx

import "testing"

// Mirrors Rust #47584: one source message reserves exactly one order, and the
// reservation is consumed when the completed message is recorded.
func TestReserveAssistantMessageOrderLikeRust(t *testing.T) {
	context := &RetainedContext{}
	first := context.ReserveAssistantMessageOrder("message-1")
	if again := context.ReserveAssistantMessageOrder("message-1"); again != first {
		t.Fatalf("same message reserved %d then %d, want one order", first, again)
	}
	second := context.ReserveAssistantMessageOrder("message-2")
	if second <= first {
		t.Fatalf("second reservation = %d, want after %d", second, first)
	}
	if got, ok := context.TakePendingAssistantMessageOrder("message-1"); !ok || got != first {
		t.Fatalf("TakePendingAssistantMessageOrder() = %d, %v; want %d, true", got, ok, first)
	}
	if _, ok := context.TakePendingAssistantMessageOrder("message-1"); ok {
		t.Fatal("reservation was consumed twice")
	}
	cloned := context.Clone()
	if got, ok := cloned.TakePendingAssistantMessageOrder("message-2"); !ok || got != second {
		t.Fatalf("clone reservation = %d, %v; want %d, true", got, ok, second)
	}
}
