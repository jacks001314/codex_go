package agentboard

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex_go/utils"
)

// TestGuardianReviewerConversationDrainsIdleInboxLikeRust mirrors Rust #62136:
// an idle reviewer's conversation admits the originals queued at that moment
// without starting a model turn, and the slot keeps the same conversation after
// a successful drain.
func TestGuardianReviewerConversationDrainsIdleInboxLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianInboxFixture(t)
	first := guardianInboxAppend(t, ctx, board, root, 1, "a", "first")
	second := guardianInboxAppend(t, ctx, board, root, 2, "b", "second")
	inbox := delivery.Inbox(reviewer)
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 1}, map[string]string{reviewer: first}); err != nil {
		t.Fatalf("Publish(1) error = %v", err)
	}
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 2}, map[string]string{reviewer: second}); err != nil {
		t.Fatalf("Publish(2) error = %v", err)
	}

	slot := &GuardianReviewerSlot{}
	conversation := NewGuardianReviewerConversation(reviewer, inbox, utils.BytesPolicy(64*1024))
	slot.Put(conversation)
	admitted := make(chan []uint64, 4)
	stop, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- slot.ObserveInbox(stop, conversation, reviewer, board, func(_ context.Context, item GuardianEvidenceItem) error {
			admitted <- guardianInboxOrdinals(t, []GuardianEvidenceItem{item})
			return nil
		})
	}()

	select {
	case ordinals := <-admitted:
		if len(ordinals) != 2 || ordinals[0] != 1 || ordinals[1] != 2 {
			t.Fatalf("admitted = %v, want [1 2] in one batch", ordinals)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle reviewer never drained its inbox")
	}
	if got := slot.CurrentThread(); got != reviewer {
		t.Fatalf("slot thread = %q, want the retained reviewer %q", got, reviewer)
	}
	if conversation.Delivered() != 2 {
		t.Fatalf("delivered = %d, want 2", conversation.Delivered())
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ObserveInbox() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("observer did not stop after cancellation")
	}
}

// TestGuardianReviewerConversationDrainsEvidenceArrivingWhileIdleLikeRust
// mirrors Rust #62136: evidence committed after the reviewer went idle wakes it
// for another drain, and each original is admitted once.
func TestGuardianReviewerConversationDrainsEvidenceArrivingWhileIdleLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianInboxFixture(t)
	inbox := delivery.Inbox(reviewer)
	slot := &GuardianReviewerSlot{}
	conversation := NewGuardianReviewerConversation(reviewer, inbox, utils.BytesPolicy(64*1024))
	slot.Put(conversation)
	admitted := make(chan []uint64, 4)
	stop, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- slot.ObserveInbox(stop, conversation, reviewer, board, func(_ context.Context, item GuardianEvidenceItem) error {
			admitted <- guardianInboxOrdinals(t, []GuardianEvidenceItem{item})
			return nil
		})
	}()
	time.Sleep(50 * time.Millisecond)

	first := guardianInboxAppend(t, ctx, board, root, 1, "a", "first")
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 1}, map[string]string{reviewer: first}); err != nil {
		t.Fatalf("Publish(1) error = %v", err)
	}
	select {
	case ordinals := <-admitted:
		if len(ordinals) != 1 || ordinals[0] != 1 {
			t.Fatalf("first admitted = %v, want [1]", ordinals)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reviewer did not drain evidence committed while idle")
	}

	second := guardianInboxAppend(t, ctx, board, root, 2, "b", "second")
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 2}, map[string]string{reviewer: second}); err != nil {
		t.Fatalf("Publish(2) error = %v", err)
	}
	select {
	case ordinals := <-admitted:
		if len(ordinals) != 1 || ordinals[0] != 2 {
			t.Fatalf("second admitted = %v, want [2]", ordinals)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reviewer did not drain the later original")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("ObserveInbox() error = %v", err)
	}
}

// TestGuardianReviewerConversationRetiresAfterFailedDeliveryLikeRust mirrors
// Rust #62136: a failed admission retires the reviewer instead of restoring a
// possibly half-written history, and a replacement conversation replays the
// inbox from the start.
func TestGuardianReviewerConversationRetiresAfterFailedDeliveryLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianInboxFixture(t)
	first := guardianInboxAppend(t, ctx, board, root, 1, "a", "first")
	second := guardianInboxAppend(t, ctx, board, root, 2, "b", "second")
	inbox := delivery.Inbox(reviewer)
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 1}, map[string]string{reviewer: first}); err != nil {
		t.Fatalf("Publish(1) error = %v", err)
	}
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 2}, map[string]string{reviewer: second}); err != nil {
		t.Fatalf("Publish(2) error = %v", err)
	}
	injected := make(chan struct{}, 4)
	admit := func(_ context.Context, _ GuardianEvidenceItem) error {
		injected <- struct{}{}
		return errors.New("injection failed")
	}
	slot := &GuardianReviewerSlot{}
	conversation := NewGuardianReviewerConversation(reviewer, inbox, utils.BytesPolicy(64*1024))
	slot.Put(conversation)
	if err := slot.ObserveInbox(ctx, conversation, reviewer, board, admit); err == nil {
		t.Fatal("ObserveInbox() reported success after admission failed")
	}
	if got := slot.CurrentThread(); got != "" {
		t.Fatalf("slot thread = %q, want the retired conversation gone", got)
	}
	if len(injected) != 1 {
		t.Fatalf("injected %d batches, want 1", len(injected))
	}

	replacement := NewGuardianReviewerConversation(reviewer+"-replacement", inbox, utils.BytesPolicy(64*1024))
	slot.Put(replacement)
	var replayed []uint64
	if err := replacement.Deliver(ctx, reviewer, board, func(_ context.Context, item GuardianEvidenceItem) error {
		replayed = append(replayed, guardianInboxOrdinals(t, []GuardianEvidenceItem{item})...)
		return nil
	}); err != nil {
		t.Fatalf("replacement Deliver() error = %v", err)
	}
	if len(replayed) != 2 || replayed[0] != 1 || replayed[1] != 2 {
		t.Fatalf("replacement replayed %v, want [1 2]", replayed)
	}
}
