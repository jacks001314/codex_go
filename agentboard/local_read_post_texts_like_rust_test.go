package agentboard

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// TestLocalBoardReadPostTextsByIDLikeRust pins Rust #62136: a trusted consumer
// reads whole bodies in the order it asked for them, one board page at a time,
// and an unknown or later post fails the read instead of shrinking it.
func TestLocalBoardReadPostTextsByIDLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	ctx := context.Background()
	board := openLocalBoard(t, sqlite, root, host)
	defer board.Close()

	if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "evidence", Subscription: Unsubscribe}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	ids := make([]string, 0, 3)
	for _, text := range []string{"first", "second", "third"} {
		metadata, err := board.Append(ctx, root, AppendRequest{
			RequestID:   text,
			Destination: PostDestination{Kind: "channel", Name: "evidence"},
			Text:        text,
		})
		if err != nil {
			t.Fatalf("Append(%s) error = %v", text, err)
		}
		ids = append(ids, metadata.MessageID)
	}

	texts, err := board.ReadPostTextsByID(ctx, root, []string{ids[2], ids[0]})
	if err != nil {
		t.Fatalf("ReadPostTextsByID() error = %v", err)
	}
	if !reflect.DeepEqual(texts, []string{"third", "first"}) {
		t.Fatalf("texts = %v, want requested order", texts)
	}

	empty, err := board.ReadPostTextsByID(ctx, root, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("ReadPostTextsByID(nil) = %v, %v", empty, err)
	}

	var invalidErr *InvalidRequestError
	if _, err := board.ReadPostTextsByID(ctx, root, []string{ids[0], "00000000-0000-0000-0000-000000000000"}); !errors.As(err, &invalidErr) {
		t.Fatalf("ReadPostTextsByID() with an unknown post = %v, want an invalid request", err)
	}

	// One board page is the batch bound, so an oversized request is refused
	// before any row is read.
	oversized := make([]string, DefaultPageLimit+1)
	for index := range oversized {
		oversized[index] = fmt.Sprintf("id-%d", index)
	}
	if _, err := board.ReadPostTextsByID(ctx, root, oversized); !errors.As(err, &invalidErr) {
		t.Fatalf("ReadPostTextsByID() over one page = %v, want an invalid request", err)
	}

	if _, err := board.ReadPostTextsByID(ctx, "123e4567-e89b-42d3-a456-4266141749ff", []string{ids[0]}); err == nil {
		t.Fatal("ReadPostTextsByID() for an unregistered caller succeeded")
	}
}

// TestReadPostTextsByIDStopsAtTheCapturedCutoffLikeRust keeps the one board read
// that a fixed view still answers bounded by its own cutoff.
func TestReadPostTextsByIDStopsAtTheCapturedCutoffLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	ctx := context.Background()
	board := openLocalBoard(t, sqlite, root, host)
	defer board.Close()

	if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "evidence", Subscription: Unsubscribe}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	before, err := board.Append(ctx, root, AppendRequest{
		RequestID:   "before",
		Destination: PostDestination{Kind: "channel", Name: "evidence"},
		Text:        "before cutoff",
	})
	if err != nil {
		t.Fatalf("Append(before) error = %v", err)
	}
	view, err := board.CaptureReadView(ctx)
	if err != nil {
		t.Fatalf("CaptureReadView() error = %v", err)
	}
	after, err := board.Append(ctx, root, AppendRequest{
		RequestID:   "after",
		Destination: PostDestination{Kind: "channel", Name: "evidence"},
		Text:        "after cutoff",
	})
	if err != nil {
		t.Fatalf("Append(after) error = %v", err)
	}

	texts, err := view.ReadPostTextsByID(ctx, root, []string{before.MessageID})
	if err != nil || !reflect.DeepEqual(texts, []string{"before cutoff"}) {
		t.Fatalf("captured read = %v, %v", texts, err)
	}
	if _, err := view.ReadPostTextsByID(ctx, root, []string{after.MessageID}); err == nil {
		t.Fatal("captured read returned a post appended after the capture")
	}
	// The live board still sees both.
	if texts, err := board.ReadPostTextsByID(ctx, root, []string{after.MessageID}); err != nil || len(texts) != 1 {
		t.Fatalf("live read after capture = %v, %v", texts, err)
	}
}
