package agentboard

import (
	"context"
	"reflect"
	"testing"
)

// TestLocalBoardAppendWithSubscribersUsesInsertionStateLikeRust pins Rust
// #62135: the housekeeper queues evidence for the subscribers present when the
// post was inserted, so a later subscribe or unsubscribe cannot decide delivery.
func TestLocalBoardAppendWithSubscribersUsesInsertionStateLikeRust(t *testing.T) {
	host, root, reviewer, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	ctx := context.Background()
	board := openLocalBoard(t, sqlite, root, host)
	defer board.Close()

	if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "release", Subscription: Unsubscribe}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	appendEvidence := func(requestID string) (*PostMetadata, []string) {
		t.Helper()
		metadata, subscribers, err := board.AppendWithSubscribers(ctx, root, AppendRequest{
			RequestID:   requestID,
			Destination: PostDestination{Kind: "channel", Name: "release"},
			Text:        "Original evidence",
		})
		if err != nil {
			t.Fatalf("AppendWithSubscribers(%q) error = %v", requestID, err)
		}
		return metadata, subscribers
	}

	// Nobody follows the channel yet.
	if _, subscribers := appendEvidence("before"); len(subscribers) != 0 {
		t.Fatalf("subscribers before any subscription = %v, want none", subscribers)
	}

	if _, err := board.SetSubscription(ctx, reviewer, SubscriptionRequest{
		Target: SubscriptionTarget{Kind: "channel", ChannelName: "release"},
		Change: Subscribe,
	}); err != nil {
		t.Fatalf("SetSubscription(subscribe) error = %v", err)
	}
	first, subscribers := appendEvidence("subscribed")
	if !reflect.DeepEqual(subscribers, []string{reviewer}) {
		t.Fatalf("subscribers at insertion = %v, want the reviewer", subscribers)
	}

	// Unsubscribing after the post must not retract what the post owed.
	if _, err := board.SetSubscription(ctx, reviewer, SubscriptionRequest{
		Target: SubscriptionTarget{Kind: "channel", ChannelName: "release"},
		Change: Unsubscribe,
	}); err != nil {
		t.Fatalf("SetSubscription(unsubscribe) error = %v", err)
	}
	second, subscribers := appendEvidence("unsubscribed")
	if len(subscribers) != 0 {
		t.Fatalf("subscribers after unsubscribe = %v, want none", subscribers)
	}
	if first.MessageID == second.MessageID {
		t.Fatal("each append must store its own post")
	}

	// An idempotent retry returns the stored post and no new deliveries.
	retried, subscribers := appendEvidence("subscribed")
	if retried.MessageID != first.MessageID {
		t.Fatalf("retry message id = %q, want %q", retried.MessageID, first.MessageID)
	}
	if len(subscribers) != 0 {
		t.Fatalf("retry subscribers = %v, want none", subscribers)
	}

	// A discussion post is delivered by the same write but reports nothing: only
	// the silent append hands its subscriber snapshot to the caller.
	posted, err := board.Post(ctx, root, PostRequest{
		RequestID:   "discussion",
		Destination: PostDestination{Kind: "channel", Name: "release"},
		Text:        "Discussion",
	})
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if posted.MessageID == "" {
		t.Fatal("Post() stored no message")
	}
}

// TestAppendWithSubscribersOnlyOnLocalBoardLikeRust keeps the capability where
// Rust puts it: the local board records subscriptions, so the other backends
// reject the request instead of guessing an empty recipient list.
func TestAppendWithSubscribersOnlyOnLocalBoardLikeRust(t *testing.T) {
	ctx := context.Background()
	host, root, _, _ := testTree(t)
	memory := (&InMemoryMessageBoards{}).Open(root, host)
	if _, _, err := memory.AppendWithSubscribers(ctx, root, AppendRequest{
		RequestID:   "memory",
		Destination: PostDestination{Kind: "new_channel", Name: "evidence"},
		Text:        "Original",
	}); err == nil {
		t.Fatal("in-memory AppendWithSubscribers() accepted a request")
	}
	var remote RemoteBoard
	if _, _, err := remote.AppendWithSubscribers(ctx, root, AppendRequest{}); err == nil {
		t.Fatal("remote AppendWithSubscribers() accepted a request")
	}
}
