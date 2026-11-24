package agentboard

import (
	"context"
	"strings"
	"testing"

	"codex_go/agent"
	"codex_go/tool"
)

// TestGuardianHousekeeperRoutesToLiveSubscribersOnlyLikeRust mirrors Rust
// #63357: committed evidence reaches only live reviewer inboxes subscribed to a
// destination channel. A persisted board subscription on a reviewer that is not
// loaded receives nothing, and a live subscription without a persisted one
// still receives the original.
func TestGuardianHousekeeperRoutesToLiveSubscribersOnlyLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianHousekeeperFixture(t)
	if err := board.UpsertChannel(ctx, root, UpsertChannelRequest{ChannelName: "topic"}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	// The root agent persists a subscription but has no live inbox selection.
	dormant := root
	if _, err := board.SetSubscription(ctx, dormant, SubscriptionRequest{
		Target: SubscriptionTarget{Kind: "channel", ChannelName: "topic"},
		Change: Subscribe,
	}); err != nil {
		t.Fatalf("persisted subscription: %v", err)
	}
	if err := delivery.Inbox(reviewer).SetSubscription("topic", true); err != nil {
		t.Fatalf("live subscription: %v", err)
	}
	housekeeper, err := NewGuardianHousekeeper(board, root, delivery, func(context.Context, GuardianRoutingBatch) (GuardianRoutingDecision, error) {
		return GuardianRoutingDecision{
			Routes:       [][]string{{"topic"}, {"topic"}},
			Descriptions: []GuardianDescriptionUpdate{{Channel: "topic", Description: "topic"}},
		}, nil
	})
	if err != nil {
		t.Fatalf("NewGuardianHousekeeper() error = %v", err)
	}
	sources := []GuardianRoutingSource{
		{ID: GuardianEvidenceID{Ordinal: 1}, Text: guardianEvidenceText(1, "one")},
		{ID: GuardianEvidenceID{Ordinal: 2}, Text: guardianEvidenceText(2, "two")},
	}
	if _, err := housekeeper.RouteNext(ctx, sources); err != nil {
		t.Fatalf("RouteNext() error = %v", err)
	}
	if queued := delivery.Inbox(reviewer).QueuedPosts(); len(queued) != 2 || queued[0].Ordinal != 1 || queued[1].Ordinal != 2 {
		t.Fatalf("live reviewer queue = %#v, want both originals", queued)
	}
	if queued := delivery.Inbox(dormant).Len(); queued != 0 {
		t.Fatalf("dormant reviewer queue = %d, want routing scoped to live reviewers", queued)
	}
}

// TestGuardianSubscriptionToolUpdatesLiveInboxLikeRust mirrors Rust #63357:
// the reviewer subscription tools change the live inbox that routing reads, and
// an unsubscribe removes the channel without dropping evidence already queued.
func TestGuardianSubscriptionToolUpdatesLiveInboxLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianHousekeeperFixture(t)
	inbox := delivery.Inbox(reviewer)
	tools := NewGuardianSubscriptionToolsWithInbox(board, reviewer, agent.AgentPathRoot, inbox, GuardianBoardToolNamespace, GuardianLiveReviewDescription)
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want subscribe and unsubscribe", len(tools))
	}
	subscribe := tools[0]
	unsubscribe := tools[1]
	if _, err := subscribe.Execute(ctx, boardInvocationRaw("subscribe", `{"channel_name":"future"}`)); err != nil {
		t.Fatalf("subscribe Execute() error = %v", err)
	}
	if got := inbox.SubscribedChannels(); len(got) != 1 || got[0] != "future" {
		t.Fatalf("live subscriptions = %v, want [future]", got)
	}

	if err := board.UpsertChannel(ctx, root, UpsertChannelRequest{ChannelName: "future"}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	messageID := guardianInboxAppend(t, ctx, board, root, 1, "future", "one")
	if delivered, err := delivery.PublishByChannel(GuardianEvidenceID{Ordinal: 1}, map[string]string{"future": messageID}); err != nil || delivered != 1 {
		t.Fatalf("PublishByChannel() = %d, %v, want the live subscriber", delivered, err)
	}
	if _, err := unsubscribe.Execute(ctx, boardInvocationRaw("unsubscribe", `{"channel_name":"future"}`)); err != nil {
		t.Fatalf("unsubscribe Execute() error = %v", err)
	}
	if got := inbox.SubscribedChannels(); len(got) != 0 {
		t.Fatalf("live subscriptions = %v, want none", got)
	}
	if queued := inbox.QueuedPosts(); len(queued) != 1 {
		t.Fatalf("queued = %#v, want the selected evidence preserved", queued)
	}
}

// TestGuardianRetrievalBindingToolsReachLiveInboxLikeRust proves the reviewer
// binding's subscription tool writes the live inbox, so the routing the
// housekeeper performs sees the reviewer's own selection.
func TestGuardianRetrievalBindingToolsReachLiveInboxLikeRust(t *testing.T) {
	ctx, board, delivery, _, reviewer := guardianHousekeeperFixture(t)
	binding, err := NewGuardianRetrievalBinding(board, reviewer, agent.AgentPathRoot)
	if err != nil {
		t.Fatalf("NewGuardianRetrievalBinding() error = %v", err)
	}
	inbox := delivery.Inbox(reviewer)
	if err := binding.BindInbox(NewGuardianLiveDelivery("other-root").Inbox(reviewer)); err == nil {
		t.Fatal("BindInbox() accepted another root's inbox")
	}
	if err := binding.BindInbox(inbox); err != nil {
		t.Fatalf("BindInbox() error = %v", err)
	}
	var subscribe tool.Executor
	for _, executor := range binding.Tools() {
		name := executor.Spec().Name
		if strings.HasSuffix(name.Key(), "."+string(Subscribe)) {
			subscribe = executor
		}
	}
	if subscribe == nil {
		t.Fatal("binding did not install the subscribe tool")
	}
	if _, err := subscribe.Execute(ctx, boardInvocationRaw("subscribe", `{"channel_name":"ops"}`)); err != nil {
		t.Fatalf("subscribe Execute() error = %v", err)
	}
	if got := inbox.SubscribedChannels(); len(got) != 1 || got[0] != "ops" {
		t.Fatalf("live subscriptions = %v, want [ops]", got)
	}
}
