package agentboard

import (
	"context"
	"strings"
	"testing"

	"codex_go/utils"
)

// TestGuardianInboxClosePreventsLaterDeliveryLikeRust mirrors Rust #63357:
// closing an inbox drops queued evidence and rejects further publishes, so a
// reviewer that reloads after shutdown starts empty.
func TestGuardianInboxClosePreventsLaterDeliveryLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianInboxFixture(t)
	messageID := guardianInboxAppend(t, ctx, board, root, 1, "a", "first")
	inbox := delivery.Inbox(reviewer)
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 1}, map[string]string{reviewer: messageID}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if inbox.Len() != 1 {
		t.Fatalf("inbox length = %d, want the queued original", inbox.Len())
	}
	wake := inbox.Subscribe()
	inbox.Close()
	if !inbox.Closed() {
		t.Fatal("CloseInbox did not close the inbox")
	}
	select {
	case <-wake:
	default:
		t.Fatal("closing the inbox did not wake its reviewer")
	}
	if inbox.Len() != 0 {
		t.Fatalf("closed inbox still holds %d originals", inbox.Len())
	}
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 2}, map[string]string{reviewer: messageID}); err == nil ||
		!strings.Contains(err.Error(), "closed") {
		t.Fatalf("Publish() on a closed inbox error = %v", err)
	}
	delivery.CloseInbox(reviewer)
	if next := delivery.Inbox(reviewer); next == inbox || next.Len() != 0 {
		t.Fatalf("reloaded reviewer inbox = %#v, want a fresh empty inbox", next)
	}
}

// TestGuardianInboxCloseRejectsPendingDeliveryLikeRust mirrors Rust #63357:
// a delivery prepared before the close cannot admit evidence afterwards.
func TestGuardianInboxCloseRejectsPendingDeliveryLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianInboxFixture(t)
	messageID := guardianInboxAppend(t, ctx, board, root, 1, "a", "first")
	inbox := delivery.Inbox(reviewer)
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 1}, map[string]string{reviewer: messageID}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	through := inbox.Len()
	delivery.CloseInbox(reviewer)
	delivered := 0
	err := inbox.Deliver(ctx, reviewer, board, &delivered, through, utils.BytesPolicy(64*1024), func(context.Context, GuardianEvidenceItem) error {
		t.Fatal("closed inbox admitted evidence")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Deliver() on a closed inbox error = %v", err)
	}
	if delivered != 0 {
		t.Fatalf("closed inbox advanced its cursor to %d", delivered)
	}
}

// TestGuardianInboxReplacementReusesQueueLikeRust mirrors Rust #63357: a
// replacement conversation for the same live agent keeps the inbox and its
// queued references.
func TestGuardianInboxReplacementReusesQueueLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianInboxFixture(t)
	messageID := guardianInboxAppend(t, ctx, board, root, 1, "a", "first")
	inbox := delivery.Inbox(reviewer)
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 1}, map[string]string{reviewer: messageID}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if again := delivery.Inbox(reviewer); again != inbox {
		t.Fatal("a replacement conversation did not reuse the live inbox")
	}
	delivered := 0
	var admitted []GuardianEvidenceItem
	if err := inbox.Deliver(ctx, reviewer, board, &delivered, inbox.Len(), utils.BytesPolicy(64*1024), func(_ context.Context, item GuardianEvidenceItem) error {
		admitted = append(admitted, item)
		return nil
	}); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if delivered != 1 || len(admitted) != 1 {
		t.Fatalf("delivered=%d admitted=%d, want the retained original", delivered, len(admitted))
	}
}
