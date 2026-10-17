package agentboard

import (
	"context"
	"errors"
	"testing"

	"codex_go/agent"
	"codex_go/state"
)

// TestLocalBoardConnectionsFixRuntimeAccessLikeRust covers #53400: one shared
// local board can grant independent runtimes read-only or read-write access,
// expose those effective permissions, and reject cross-agent subscription
// changes from a read-only connection.
func TestLocalBoardConnectionsFixRuntimeAccessLikeRust(t *testing.T) {
	ctx := context.Background()
	host := newTestHost(t, map[string]agent.AgentPath{
		"writer-thread": agent.AgentPath("/root/writer"),
		"reader-thread": agent.AgentPath("/root/reader"),
	})
	sqlite, err := state.NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	shared := NewLocalBoard(sqlite, "shared-board")
	writerConnection := shared.ReadWrite()
	readerConnection := shared.ReadOnly()
	writer, err := writerConnection.Open(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := readerConnection.Open(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if !writerConnection.SameBoard(readerConnection) {
		t.Fatal("connections opened from one LocalBoard did not share a canonical database")
	}
	readerPath, err := readerConnection.CallerPath("reader-thread")
	if err != nil || readerPath != agent.AgentPath("/root/readerthread") {
		t.Fatalf("reader caller path = %q, %v", readerPath, err)
	}

	if _, err := writer.CreateChannel(ctx, "writer-thread", CreateChannelRequest{
		ChannelName: "support", Subscription: Subscribe,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Post(ctx, "writer-thread", PostRequest{
		RequestID: "root", Destination: PostDestination{Kind: "channel", Name: "support"}, Text: "hello",
	}); err != nil {
		t.Fatal(err)
	}

	channels, err := reader.ListChannels(ctx, "reader-thread", ChannelQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if channels.BoardPermissions == nil || channels.BoardPermissions.CreateChannel {
		t.Fatalf("read-only board permissions = %#v", channels.BoardPermissions)
	}
	if len(channels.Results) != 1 || channels.Results[0].Permissions == nil ||
		!channels.Results[0].Permissions.Read || channels.Results[0].Permissions.Post || channels.Results[0].Permissions.Reply {
		t.Fatalf("read-only channel permissions = %#v", channels.Results)
	}

	_, err = reader.Post(ctx, "reader-thread", PostRequest{
		RequestID: "denied", Destination: PostDestination{Kind: "channel", Name: "support"}, Text: "no",
	})
	var denied *PermissionDenied
	if !errors.As(err, &denied) || denied.Action == nil || *denied.Action != "channel.post" {
		t.Fatalf("read-only post = %#v, %v", denied, err)
	}
	if _, err := reader.SetSubscription(ctx, "reader-thread", SubscriptionRequest{
		Target: SubscriptionTarget{Kind: "channel", ChannelName: "support"}, Change: Subscribe,
	}); err != nil {
		t.Fatalf("read-only own subscription: %v", err)
	}
	other := agent.AgentPath("/root/writer")
	_, err = reader.SetSubscription(ctx, "reader-thread", SubscriptionRequest{
		Target: SubscriptionTarget{Kind: "channel", ChannelName: "support"}, TargetAgent: &other, Change: Unsubscribe,
	})
	if !errors.As(err, &denied) || denied.Action == nil || *denied.Action != "board.manage_subscriptions" {
		t.Fatalf("read-only cross-agent subscription = %#v, %v", denied, err)
	}

	otherSQLite, err := state.NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	otherConnection := NewLocalBoard(otherSQLite, "shared-board").ReadOnly()
	otherBoard, err := otherConnection.Open(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	defer otherBoard.Close()
	if writerConnection.SameBoard(otherConnection) {
		t.Fatal("same identity with another canonical database reported the same board")
	}
}

func TestLegacyLocalBoardKeepsUnrestrictedShapeLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	sqlite, err := state.NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	board, err := OpenLocalBoard(context.Background(), sqlite, "legacy-board", host)
	if err != nil {
		t.Fatal(err)
	}
	defer board.Close()
	if _, err := board.CreateChannel(context.Background(), root, CreateChannelRequest{ChannelName: "legacy"}); err != nil {
		t.Fatal(err)
	}
	page, err := board.ListChannels(context.Background(), root, ChannelQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.BoardPermissions != nil || len(page.Results) != 1 || page.Results[0].Permissions != nil {
		t.Fatalf("legacy channel page = %#v", page)
	}
}
