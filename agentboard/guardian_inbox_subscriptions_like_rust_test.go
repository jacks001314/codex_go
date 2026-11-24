package agentboard

import (
	"context"
	"testing"

	"codex_go/utils"
)

// TestGuardianInboxSubscribesBeforeChannelExistsLikeRust mirrors Rust #63357:
// an exact channel name can be subscribed to before the channel exists, the
// selection is listed in sorted order, and evidence routed to that channel
// reaches the live inbox.
func TestGuardianInboxSubscribesBeforeChannelExistsLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianInboxFixture(t)
	inbox := delivery.Inbox(reviewer)
	for _, channel := range []string{"later", "a"} {
		if err := inbox.SetSubscription(channel, true); err != nil {
			t.Fatalf("SetSubscription(%q) error = %v", channel, err)
		}
	}
	if got := inbox.SubscribedChannels(); len(got) != 2 || got[0] != "a" || got[1] != "later" {
		t.Fatalf("SubscribedChannels() = %v, want sorted [a later]", got)
	}
	if err := inbox.SetSubscription("", true); err == nil {
		t.Fatal("SetSubscription() accepted an empty channel")
	}

	messageID := guardianInboxAppend(t, ctx, board, root, 1, "a", "first")
	delivered, err := delivery.PublishByChannel(GuardianEvidenceID{Ordinal: 1}, map[string]string{"a": messageID})
	if err != nil {
		t.Fatalf("PublishByChannel() error = %v", err)
	}
	if delivered != 1 || inbox.Len() != 1 {
		t.Fatalf("delivered=%d queued=%d, want the subscribed live inbox once", delivered, inbox.Len())
	}
}

// TestGuardianInboxPublishesOncePerMatchingInboxLikeRust mirrors Rust #63357:
// one original routed through several subscribed channels reaches an inbox
// only once, while unsubscribing preserves the evidence already selected.
func TestGuardianInboxPublishesOncePerMatchingInboxLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianInboxFixture(t)
	inbox := delivery.Inbox(reviewer)
	for _, channel := range []string{"a", "b"} {
		if err := inbox.SetSubscription(channel, true); err != nil {
			t.Fatalf("SetSubscription(%q) error = %v", channel, err)
		}
	}
	first := guardianInboxAppend(t, ctx, board, root, 1, "a", "first")
	second := guardianInboxAppend(t, ctx, board, root, 1, "b", "first")
	delivered, err := delivery.PublishByChannel(GuardianEvidenceID{Ordinal: 1}, map[string]string{"a": first, "b": second})
	if err != nil {
		t.Fatalf("PublishByChannel() error = %v", err)
	}
	if delivered != 1 || inbox.Len() != 1 {
		t.Fatalf("delivered=%d queued=%d, want one original per inbox", delivered, inbox.Len())
	}
	if queued := inbox.QueuedPosts(); len(queued) != 1 || queued[0].MessageID != first {
		t.Fatalf("queued = %#v, want the first channel's post", queued)
	}

	if err := inbox.SetSubscription("a", false); err != nil {
		t.Fatalf("unsubscribe error = %v", err)
	}
	if got := inbox.SubscribedChannels(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("SubscribedChannels() = %v, want [b]", got)
	}
	deliveredCount := 0
	if err := inbox.Deliver(ctx, reviewer, board, &deliveredCount, inbox.Len(), utils.BytesPolicy(64*1024), func(_ context.Context, _ GuardianEvidenceItem) error {
		return nil
	}); err != nil {
		t.Fatalf("Deliver() after unsubscribe error = %v", err)
	}
	if deliveredCount != 1 {
		t.Fatalf("delivered after unsubscribe = %d, want the selected original preserved", deliveredCount)
	}
}

// TestGuardianInboxCloseDropsSubscriptionsLikeRust mirrors Rust #63357: closing
// an inbox removes its channel selections, and a reloaded reviewer for the same
// agent starts with an empty subscription set and inbox.
func TestGuardianInboxCloseDropsSubscriptionsLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianInboxFixture(t)
	inbox := delivery.Inbox(reviewer)
	if err := inbox.SetSubscription("a", true); err != nil {
		t.Fatalf("SetSubscription() error = %v", err)
	}
	delivery.CloseInbox(reviewer)
	reloaded := delivery.Inbox(reviewer)
	if reloaded == inbox {
		t.Fatal("CloseInbox() kept the retired inbox")
	}
	if got := reloaded.SubscribedChannels(); len(got) != 0 {
		t.Fatalf("reloaded subscriptions = %v, want none", got)
	}
	messageID := guardianInboxAppend(t, ctx, board, root, 1, "a", "first")
	delivered, err := delivery.PublishByChannel(GuardianEvidenceID{Ordinal: 1}, map[string]string{"a": messageID})
	if err != nil {
		t.Fatalf("PublishByChannel() error = %v", err)
	}
	if delivered != 0 || reloaded.Len() != 0 {
		t.Fatalf("delivered=%d queued=%d, want a retired subscription to route nothing", delivered, reloaded.Len())
	}
	if err := inbox.SetSubscription("b", true); err == nil {
		t.Fatal("closed inbox accepted a subscription")
	}
}
