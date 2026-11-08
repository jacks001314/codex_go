package agentboard

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func bulkSubscribedChannels(t *testing.T, board *LocalAgentMessageBoard, ctx context.Context, caller string) []string {
	t.Helper()
	page, err := board.SubscribedChannels(ctx, caller, PageRequest{Limit: 50})
	if err != nil {
		t.Fatalf("SubscribedChannels() error = %v", err)
	}
	return append([]string{}, page.Results...)
}

// TestLocalBoardBulkSubscriptionsAreAtomicLikeRust pins Rust #59998: a bulk
// membership request is validated in full before any row is written, so one
// unknown channel fails the whole call, and only the difference from the stored
// membership is applied.
func TestLocalBoardBulkSubscriptionsAreAtomicLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	ctx := context.Background()
	board := openLocalBoard(t, sqlite, root, host)
	defer board.Close()

	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("bulk-%d", i)
		// Unsubscribe keeps the fixture empty: the author is not subscribed by
		// creating the channel.
		if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: name, Subscription: Unsubscribe}); err != nil {
			t.Fatalf("CreateChannel(%s) error = %v", name, err)
		}
	}
	if channels := bulkSubscribedChannels(t, board, ctx, root); len(channels) != 0 {
		t.Fatalf("fixture subscriptions = %v, want none", channels)
	}

	// The third target does not exist, so nothing is written - not even for the
	// two channels that do exist.
	_, err := board.SetSubscriptions(ctx, root, BulkSubscriptionsRequest{
		Channels: []string{"bulk-1", "bulk-2", "bulk-missing", "bulk-4", "bulk-5"},
	})
	var invalidErr *InvalidRequestError
	if !errors.As(err, &invalidErr) {
		t.Fatalf("SetSubscriptions() with an unknown channel = %v, want an invalid request", err)
	}
	if channels := bulkSubscribedChannels(t, board, ctx, root); len(channels) != 0 {
		t.Fatalf("subscriptions after a failed bulk request = %v, want none written", channels)
	}

	// A valid request writes exactly the requested membership.
	state, err := board.SetSubscriptions(ctx, root, BulkSubscriptionsRequest{Channels: []string{"bulk-4", "bulk-2"}})
	if err != nil {
		t.Fatalf("SetSubscriptions() error = %v", err)
	}
	if !reflect.DeepEqual(state.Channels, []string{"bulk-2", "bulk-4"}) {
		t.Fatalf("state.Channels = %v, want the sorted membership", state.Channels)
	}
	if channels := bulkSubscribedChannels(t, board, ctx, root); !reflect.DeepEqual(channels, []string{"bulk-2", "bulk-4"}) {
		t.Fatalf("subscriptions = %v, want [bulk-2 bulk-4]", channels)
	}

	// The next request only differs by one channel, and the dropped channel is
	// opted out so posting there does not subscribe the agent again.
	if _, err := board.SetSubscriptions(ctx, root, BulkSubscriptionsRequest{Channels: []string{"bulk-2", "bulk-4", "bulk-5"}}); err != nil {
		t.Fatalf("SetSubscriptions(grow) error = %v", err)
	}
	if channels := bulkSubscribedChannels(t, board, ctx, root); !reflect.DeepEqual(channels, []string{"bulk-2", "bulk-4", "bulk-5"}) {
		t.Fatalf("subscriptions after grow = %v", channels)
	}
	if _, err := board.SetSubscriptions(ctx, root, BulkSubscriptionsRequest{Channels: []string{"bulk-4"}}); err != nil {
		t.Fatalf("SetSubscriptions(shrink) error = %v", err)
	}
	if channels := bulkSubscribedChannels(t, board, ctx, root); !reflect.DeepEqual(channels, []string{"bulk-4"}) {
		t.Fatalf("subscriptions after shrink = %v, want [bulk-4]", channels)
	}
	if _, err := board.Post(ctx, root, PostRequest{RequestID: "bulk-opt-out", Destination: PostDestination{Kind: "channel", Name: "bulk-2"}, Text: "root in a dropped channel"}); err != nil {
		t.Fatalf("Post(bulk-2) error = %v", err)
	}
	if channels := bulkSubscribedChannels(t, board, ctx, root); !reflect.DeepEqual(channels, []string{"bulk-4"}) {
		t.Fatalf("subscriptions after posting to a dropped channel = %v, want the opt-out kept", channels)
	}

	// An empty membership unsubscribes everything.
	if _, err := board.SetSubscriptions(ctx, root, BulkSubscriptionsRequest{}); err != nil {
		t.Fatalf("SetSubscriptions(empty) error = %v", err)
	}
	if channels := bulkSubscribedChannels(t, board, ctx, root); len(channels) != 0 {
		t.Fatalf("subscriptions after an empty request = %v, want none", channels)
	}
}
