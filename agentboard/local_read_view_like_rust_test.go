package agentboard

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// TestLocalBoardFixedReadViewFreezesCatalogAndCutoffLikeRust pins Rust #59993:
// the view freezes the channel catalog and the post sequence ceiling, serves
// only rows at or below the cutoff - a backdated append stays invisible - and
// rejects every mutation.
func TestLocalBoardFixedReadViewFreezesCatalogAndCutoffLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	ctx := context.Background()
	board := openLocalBoard(t, sqlite, root, host)
	defer board.Close()

	for _, name := range []string{"alpha", "beta"} {
		if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: name, Subscription: Unsubscribe}); err != nil {
			t.Fatalf("CreateChannel(%s) error = %v", name, err)
		}
	}
	first, err := board.Post(ctx, root, PostRequest{RequestID: "view-1", Destination: PostDestination{Kind: "channel", Name: "alpha"}, Text: "before the view"})
	if err != nil {
		t.Fatalf("Post(before) error = %v", err)
	}

	view, err := board.CaptureReadView(ctx)
	if err != nil {
		t.Fatalf("CaptureReadView() error = %v", err)
	}
	if view.Cutoff() == 0 {
		t.Fatal("Cutoff() = 0, want the sequence of the captured posts")
	}

	// The catalog is frozen: a channel created after the capture is invisible.
	if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "gamma", Subscription: Unsubscribe}); err != nil {
		t.Fatalf("CreateChannel(gamma) error = %v", err)
	}
	if channels := view.Channels(); !reflect.DeepEqual(channels, []string{"alpha", "beta"}) {
		t.Fatalf("view channels = %v, want the frozen catalog [alpha beta]", channels)
	}

	// The cutoff is frozen: a post appended afterwards is invisible, and
	// backdating it does not smuggle it in.
	late, err := board.Post(ctx, root, PostRequest{RequestID: "view-2", Destination: PostDestination{Kind: "channel", Name: "alpha"}, Text: "after the view"})
	if err != nil {
		t.Fatalf("Post(after) error = %v", err)
	}
	if _, err := board.db.ExecContext(ctx, `UPDATE posts SET timestamp = 1 WHERE id = ?`, late.MessageID); err != nil {
		t.Fatalf("backdate post: %v", err)
	}
	if _, err := view.ReadPost(ctx, root, ReadPostRequest{MessageID: late.MessageID}); err == nil {
		t.Fatal("the view served a backdated post appended after the capture")
	}
	if _, err := board.ReadPost(ctx, root, ReadPostRequest{MessageID: late.MessageID}); err != nil {
		t.Fatalf("the live board lost the backdated post: %v", err)
	}
	if _, err := view.ReadPost(ctx, root, ReadPostRequest{MessageID: first.MessageID}); err != nil {
		t.Fatalf("the view lost a post captured inside the cutoff: %v", err)
	}

	// Every mutation is rejected.
	if _, err := view.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "delta"}); !errors.Is(err, ErrBoardReadViewReadOnly) {
		t.Fatalf("view CreateChannel() error = %v, want read-only", err)
	}
	if err := view.UpsertChannel(ctx, root, UpsertChannelRequest{ChannelName: "alpha"}); !errors.Is(err, ErrBoardReadViewReadOnly) {
		t.Fatalf("view UpsertChannel() error = %v, want read-only", err)
	}
	if _, err := view.Post(ctx, root, PostRequest{RequestID: "view-3", Destination: PostDestination{Kind: "channel", Name: "alpha"}, Text: "nope"}); !errors.Is(err, ErrBoardReadViewReadOnly) {
		t.Fatalf("view Post() error = %v, want read-only", err)
	}
	if _, err := view.Append(ctx, root, AppendRequest{RequestID: "view-4", Destination: PostDestination{Kind: "channel", Name: "alpha"}, Text: "nope"}); !errors.Is(err, ErrBoardReadViewReadOnly) {
		t.Fatalf("view Append() error = %v, want read-only", err)
	}
	if _, err := view.SetSubscription(ctx, root, SubscriptionRequest{Target: SubscriptionTarget{Kind: "channel", ChannelName: "alpha"}, Change: Subscribe}); !errors.Is(err, ErrBoardReadViewReadOnly) {
		t.Fatalf("view SetSubscription() error = %v, want read-only", err)
	}
	if _, err := view.SetSubscriptions(ctx, root, BulkSubscriptionsRequest{Channels: []string{"alpha"}}); !errors.Is(err, ErrBoardReadViewReadOnly) {
		t.Fatalf("view SetSubscriptions() error = %v, want read-only", err)
	}
}
