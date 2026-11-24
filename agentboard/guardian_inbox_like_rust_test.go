package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"codex_go/utils"
)

// guardianEvidenceText is the stored body of one routed original: the source
// ordinal plus the exact model-visible item (Rust RoutedOriginal).
func guardianEvidenceText(ordinal uint64, output string) string {
	return fmt.Sprintf(
		`{"id":{"ordinal":%d},"original":{"type":"response_item","payload":{"type":"function_call_output","output":%q}}}`,
		ordinal, output,
	)
}

func guardianInboxFixture(t *testing.T) (context.Context, *LocalAgentMessageBoard, *GuardianLiveDelivery, string, string) {
	t.Helper()
	ctx := context.Background()
	host, root, reviewer, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	board, err := NewGuardianLocalBoard(sqlite, root).ReadWrite().Open(ctx, host)
	if err != nil {
		t.Fatalf("open guardian board: %v", err)
	}
	t.Cleanup(board.Close)
	for _, name := range []string{"a", "b"} {
		if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: name, Subscription: Unsubscribe}); err != nil {
			t.Fatalf("create channel %q: %v", name, err)
		}
	}
	return ctx, board, NewGuardianLiveDelivery(root), root, reviewer
}

// guardianInboxAppend stores one routed original and returns its post ID.
func guardianInboxAppend(t *testing.T, ctx context.Context, board *LocalAgentMessageBoard, root string, ordinal uint64, channel string, output string) string {
	t.Helper()
	requestID, err := GuardianEvidenceRequestID(ordinal, channel)
	if err != nil {
		t.Fatalf("GuardianEvidenceRequestID(%d, %q) error = %v", ordinal, channel, err)
	}
	metadata, err := board.Append(ctx, root, AppendRequest{
		RequestID:   requestID,
		Destination: PostDestination{Kind: "channel", Name: channel},
		Text:        guardianEvidenceText(ordinal, output),
	})
	if err != nil {
		t.Fatalf("append evidence %d to %q: %v", ordinal, channel, err)
	}
	return metadata.MessageID
}

func guardianInboxOrdinals(t *testing.T, items []GuardianEvidenceItem) []uint64 {
	t.Helper()
	ordinals := make([]uint64, 0, len(items))
	for _, item := range items {
		if item.Name != GuardianEvidenceContextName || item.Namespace != GuardianEvidenceNamespace {
			t.Fatalf("evidence item = %#v", item)
		}
		var payload struct {
			Sources []struct {
				ID struct {
					Ordinal uint64 `json:"ordinal"`
				} `json:"id"`
			} `json:"sources"`
		}
		if err := json.Unmarshal([]byte(item.Text), &payload); err != nil {
			t.Fatalf("decode evidence item: %v (%q)", err, item.Text)
		}
		for _, source := range payload.Sources {
			ordinals = append(ordinals, source.ID.Ordinal)
		}
	}
	return ordinals
}

// TestGuardianInboxDeliversQueuedEvidenceInSourceOrderLikeRust mirrors Rust
// guardian-v3 #62136: the inbox queues one post per derived ordinal, admits them
// in source order through the reviewer's budget, and refuses evidence that
// arrives out of order.
func TestGuardianInboxDeliversQueuedEvidenceInSourceOrderLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianInboxFixture(t)

	first := guardianInboxAppend(t, ctx, board, root, 1, "a", "one")
	second := guardianInboxAppend(t, ctx, board, root, 2, "b", "two")
	third := guardianInboxAppend(t, ctx, board, root, 3, "a", "three")
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 1}, map[string]string{reviewer: first}); err != nil {
		t.Fatalf("Publish(1) error = %v", err)
	}
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 2}, map[string]string{reviewer: second}); err != nil {
		t.Fatalf("Publish(2) error = %v", err)
	}
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 3}, map[string]string{reviewer: third}); err != nil {
		t.Fatalf("Publish(3) error = %v", err)
	}
	inbox := delivery.Inbox(reviewer)
	if inbox.Len() != 3 {
		t.Fatalf("queued evidence = %d, want 3", inbox.Len())
	}
	queued := inbox.QueuedPosts()
	for index, want := range []uint64{1, 2, 3} {
		if queued[index].Ordinal != want {
			t.Fatalf("queued evidence = %#v, want ordinals 1,2,3", queued)
		}
	}

	var admitted []GuardianEvidenceItem
	admit := func(_ context.Context, item GuardianEvidenceItem) error {
		admitted = append(admitted, item)
		return nil
	}
	delivered := 0
	if err := inbox.Deliver(ctx, reviewer, board, &delivered, inbox.Len(), utils.BytesPolicy(64*1024), admit); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if delivered != 3 {
		t.Fatalf("delivered cursor = %d, want 3", delivered)
	}
	if got := guardianInboxOrdinals(t, admitted); !reflect.DeepEqual(got, []uint64{1, 2, 3}) {
		t.Fatalf("admitted ordinals = %v, want source order", got)
	}

	// The same cursor owes nothing more, and a replacement conversation replays
	// the queue from the start.
	before := len(admitted)
	if err := inbox.Deliver(ctx, reviewer, board, &delivered, inbox.Len(), utils.BytesPolicy(64*1024), admit); err != nil {
		t.Fatalf("second Deliver() error = %v", err)
	}
	if len(admitted) != before {
		t.Fatalf("second Deliver() admitted %d more items", len(admitted)-before)
	}
	replacement := 0
	if err := inbox.Deliver(ctx, reviewer, board, &replacement, inbox.Len(), utils.BytesPolicy(64*1024), admit); err != nil {
		t.Fatalf("replacement Deliver() error = %v", err)
	}
	if replacement != 3 {
		t.Fatalf("replacement cursor = %d, want 3", replacement)
	}

	// Republishing the original that already heads the queue is idempotent
	// (Rust #63357 routes one original once per matching inbox), while routing
	// an older original would go backwards.
	queuedBefore := len(inbox.QueuedPosts())
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 3}, map[string]string{reviewer: third}); err != nil {
		t.Fatalf("idempotent Publish() error = %v", err)
	}
	if len(inbox.QueuedPosts()) != queuedBefore {
		t.Fatal("idempotent Publish() queued the original twice")
	}
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 2}, map[string]string{reviewer: second}); err == nil {
		t.Fatal("Publish() accepted evidence older than the queue head")
	}
}

// TestGuardianInboxRetainsEvidenceAcrossSubscriptionChangesLikeRust proves the
// queue is independent of the live subscription: unsubscribing keeps what was
// already routed, a failed admission does not advance the cursor, and an
// original larger than the budget is refused instead of truncated.
func TestGuardianInboxRetainsEvidenceAcrossSubscriptionChangesLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianInboxFixture(t)
	inbox := delivery.Inbox(reviewer)

	small := guardianInboxAppend(t, ctx, board, root, 1, "a", "one")
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 1}, map[string]string{reviewer: small}); err != nil {
		t.Fatalf("Publish(1) error = %v", err)
	}
	if _, err := board.SetSubscription(ctx, reviewer, SubscriptionRequest{
		Target: SubscriptionTarget{Kind: "channel", ChannelName: "a"},
		Change: Unsubscribe,
	}); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if inbox.Len() != 1 {
		t.Fatalf("queued evidence after unsubscribe = %d, want the routed original", inbox.Len())
	}

	delivered := 0
	if err := inbox.Deliver(ctx, reviewer, board, &delivered, inbox.Len(), utils.BytesPolicy(64*1024), func(context.Context, GuardianEvidenceItem) error {
		return errors.New("admission unavailable")
	}); err == nil {
		t.Fatal("Deliver() accepted a failed admission")
	}
	if delivered != 0 {
		t.Fatalf("cursor after a failed admission = %d, want 0", delivered)
	}

	// A single original that cannot fit the model budget is refused rather than
	// truncated, so the reviewer never reads a half-evidence body.
	large := guardianInboxAppend(t, ctx, board, root, 2, "a", "oversized")
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 2}, map[string]string{reviewer: large}); err != nil {
		t.Fatalf("Publish(2) error = %v", err)
	}
	if err := inbox.Deliver(ctx, reviewer, board, &delivered, inbox.Len(), utils.BytesPolicy(8), func(context.Context, GuardianEvidenceItem) error {
		return nil
	}); err == nil {
		t.Fatal("Deliver() admitted evidence larger than the model budget")
	}
	if delivered != 0 {
		t.Fatalf("cursor after the budget refusal = %d, want 0", delivered)
	}
}

// TestGuardianInboxRejectsWrongBoardOrCallerLikeRust keeps one inbox bound to
// the board and actor it was opened for.
func TestGuardianInboxRejectsWrongBoardOrCallerLikeRust(t *testing.T) {
	ctx, board, delivery, _, reviewer := guardianInboxFixture(t)
	inbox := delivery.Inbox(reviewer)
	delivered := 0
	admit := func(context.Context, GuardianEvidenceItem) error { return nil }
	if err := inbox.Deliver(ctx, "123e4567-e89b-42d3-a456-4266141749ff", board, &delivered, 0, utils.BytesPolicy(1024), admit); err == nil {
		t.Fatal("Deliver() accepted a different actor")
	}
	if err := inbox.Deliver(ctx, reviewer, nil, &delivered, 0, utils.BytesPolicy(1024), admit); err == nil {
		t.Fatal("Deliver() accepted a missing board")
	}
	if err := inbox.Deliver(ctx, reviewer, board, nil, 0, utils.BytesPolicy(1024), admit); err == nil {
		t.Fatal("Deliver() accepted a missing cursor")
	}
	if err := inbox.Deliver(ctx, reviewer, board, &delivered, 1, utils.BytesPolicy(1024), admit); err == nil {
		t.Fatal("Deliver() accepted a target beyond the queue")
	}
}
