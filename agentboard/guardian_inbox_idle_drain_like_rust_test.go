package agentboard

import (
	"context"
	"testing"

	"codex_go/utils"
)

// TestGuardianInboxDeliverPendingDrainsWhenIdleLikeRust mirrors Rust #62136: an
// idle reviewer admits every original queued at that moment, and evidence that
// arrives during the drain waits for the next one.
func TestGuardianInboxDeliverPendingDrainsWhenIdleLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianInboxFixture(t)
	first := guardianInboxAppend(t, ctx, board, root, 1, "a", "first")
	second := guardianInboxAppend(t, ctx, board, root, 2, "a", "second")
	inbox := delivery.Inbox(reviewer)
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 1}, map[string]string{reviewer: first}); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 2}, map[string]string{reviewer: second}); err != nil {
		t.Fatalf("Publish(second) error = %v", err)
	}

	delivered := 0
	var admitted []GuardianEvidenceItem
	late := guardianInboxAppend(t, ctx, board, root, 3, "a", "late")
	if err := inbox.DeliverPending(ctx, reviewer, board, &delivered, utils.BytesPolicy(64*1024), func(_ context.Context, item GuardianEvidenceItem) error {
		admitted = append(admitted, item)
		if len(admitted) == 1 {
			// Evidence arriving while the reviewer admits must wait for the
			// next idle drain.
			if err := delivery.Publish(GuardianEvidenceID{Ordinal: 3}, map[string]string{reviewer: late}); err != nil {
				t.Fatalf("Publish(late) error = %v", err)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("DeliverPending() error = %v", err)
	}
	if delivered != 2 {
		t.Fatalf("delivered=%d, want the two observed originals", delivered)
	}
	if got := guardianInboxOrdinals(t, admitted); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("admitted originals = %v, want [1 2]", got)
	}
	if inbox.Len() != 3 {
		t.Fatalf("inbox length = %d, want the late original queued", inbox.Len())
	}

	if err := inbox.DeliverPending(ctx, reviewer, board, &delivered, utils.BytesPolicy(64*1024), func(_ context.Context, item GuardianEvidenceItem) error {
		admitted = append(admitted, item)
		return nil
	}); err != nil {
		t.Fatalf("second DeliverPending() error = %v", err)
	}
	if delivered != 3 {
		t.Fatalf("after second drain delivered=%d, want 3", delivered)
	}
	if got := guardianInboxOrdinals(t, admitted); len(got) != 3 || got[2] != 3 {
		t.Fatalf("admitted originals = %v, want the late original last", got)
	}
}
