package agentboard

import (
	"strings"
	"testing"
)

// TestGuardianLiveDeliveriesReuseRootLikeRust mirrors Rust #63361: a root
// reload reuses the live delivery, so a surviving child reviewer keeps its
// subscriptions and queued evidence.
func TestGuardianLiveDeliveriesReuseRootLikeRust(t *testing.T) {
	registry := NewGuardianLiveDeliveries()
	first := registry.ForRoot("root-a")
	inbox := first.Inbox("child")
	if err := first.Publish(GuardianEvidenceID{Ordinal: 3}, map[string]string{"child": "post-3"}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	reloaded, ok := registry.Existing("root-a")
	if !ok || reloaded != first {
		t.Fatal("root reload did not find the live delivery")
	}
	if again := registry.ForRoot("root-a"); again != first {
		t.Fatal("root reload created a fresh delivery stream")
	}
	if again := reloaded.Inbox("child"); again != inbox || again.Len() != 1 {
		t.Fatalf("surviving reviewer inbox = %#v, want the queued original", again.QueuedPosts())
	}
	if other := registry.ForRoot("root-b"); other == first {
		t.Fatal("different roots shared one delivery stream")
	}
}

// TestGuardianLiveDeliveriesRetireLikeRust mirrors Rust #63361's shutdown
// rule: a root reloaded after shutdown starts with an empty inbox set.
func TestGuardianLiveDeliveriesRetireLikeRust(t *testing.T) {
	registry := NewGuardianLiveDeliveries()
	delivery := registry.ForRoot("root-a")
	inbox := delivery.Inbox("child")
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 1}, map[string]string{"child": "post-1"}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	registry.Retire("root-a")
	if !inbox.Closed() {
		t.Fatal("retiring the root did not close the reviewer inbox")
	}
	if _, ok := registry.Existing("root-a"); ok {
		t.Fatal("retired root stayed registered")
	}
	fresh := registry.ForRoot("root-a")
	if fresh == delivery {
		t.Fatal("reloaded root reused the retired delivery")
	}
	if len(fresh.Inbox("child").QueuedPosts()) != 0 {
		t.Fatal("reloaded root started with queued evidence")
	}

	other := registry.ForRoot("root-b")
	otherInbox := other.Inbox("child")
	registry.RetireAll()
	if !otherInbox.Closed() {
		t.Fatal("RetireAll did not close every root's inboxes")
	}
	if _, ok := registry.Existing("root-b"); ok {
		t.Fatal("RetireAll left a root registered")
	}
	if err := other.Publish(GuardianEvidenceID{Ordinal: 2}, map[string]string{"child": "post-2"}); err == nil ||
		!strings.Contains(err.Error(), "closed") {
		t.Fatalf("publish after RetireAll error = %v", err)
	}
}
