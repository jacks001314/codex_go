package agentboard

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/agent"
	"codex_go/tool"
)

// Rust #56494: an upsert creates a channel or replaces only its description.
// Existing posts, authorship, creation time, and subscriptions survive.
func TestInMemoryUpsertChannelPreservesEvidenceLikeRust(t *testing.T) {
	ctx := context.Background()
	host := newTestHost(t, map[string]agent.AgentPath{
		"root-thread":  agent.AgentPathRoot,
		"child-thread": agent.AgentPath("/root/child"),
	})
	board := (&InMemoryMessageBoards{}).Open("board-1", host)

	created, err := board.CreateChannel(ctx, "root-thread", CreateChannelRequest{
		ChannelName:  "work",
		Description:  pointerTo("Initial purpose"),
		Subscription: Subscribe,
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if created.Description == nil || *created.Description != "Initial purpose" {
		t.Fatalf("created.Description = %v", created.Description)
	}
	if _, err := board.Post(ctx, "root-thread", PostRequest{
		RequestID:   "turn-1:call-1",
		Destination: PostDestination{Kind: "channel", Name: "work"},
		Text:        "hello",
	}); err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	if err := board.UpsertChannel(ctx, "child-thread", UpsertChannelRequest{
		ChannelName: "work",
		Description: "Shared policies",
	}); err != nil {
		t.Fatalf("UpsertChannel(update) error = %v", err)
	}
	updated := inMemoryChannel(t, board, "work")
	if updated.Description == nil || *updated.Description != "Shared policies" {
		t.Fatalf("updated.Description = %v", updated.Description)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) || updated.CreatedBy != created.CreatedBy {
		t.Fatalf("upsert replaced channel metadata: %#v", updated)
	}
	if updated.MessageCount != 1 {
		t.Fatalf("upsert changed message count: %#v", updated)
	}

	if err := board.UpsertChannel(ctx, "root-thread", UpsertChannelRequest{
		ChannelName: "fresh",
		Description: "Fresh purpose",
	}); err != nil {
		t.Fatalf("UpsertChannel(create) error = %v", err)
	}
	fresh := inMemoryChannel(t, board, "fresh")
	if fresh.Description == nil || *fresh.Description != "Fresh purpose" || fresh.CreatedBy != agent.AgentPathRoot {
		t.Fatalf("fresh = %#v", fresh)
	}

	if err := board.UpsertChannel(ctx, "root-thread", UpsertChannelRequest{
		ChannelName: "fresh",
		Description: strings.Repeat("x", channelDescriptionMaxBytes+1),
	}); err == nil || !IsInvalidRequest(err) {
		t.Fatalf("oversized description error = %v", err)
	}
	if _, err := board.CreateChannel(ctx, "root-thread", CreateChannelRequest{
		ChannelName:  "too-long",
		Description:  pointerTo(strings.Repeat("x", channelDescriptionMaxBytes+1)),
		Subscription: Subscribe,
	}); err == nil || !IsInvalidRequest(err) {
		t.Fatalf("oversized create description error = %v", err)
	}
}

func inMemoryChannel(t *testing.T, board *InMemoryBoard, name string) ChannelSummary {
	t.Helper()
	page, err := board.ListChannels(context.Background(), "root-thread", ChannelQuery{})
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	for _, channel := range page.Results {
		if channel.ChannelName == name {
			return channel
		}
	}
	t.Fatalf("channel %q not listed", name)
	return ChannelSummary{}
}

func TestLocalBoardUpsertChannelPreservesEvidenceLikeRust(t *testing.T) {
	ctx := context.Background()
	host, root, _, _ := testTree(t)
	shared := NewLocalBoard(openBoardSqliteConfig(t), root)
	writer, err := shared.ReadWrite().Open(ctx, host)
	if err != nil {
		t.Fatalf("Open(writer) error = %v", err)
	}
	defer writer.Close()

	created, err := writer.CreateChannel(ctx, root, CreateChannelRequest{
		ChannelName:  "work",
		Description:  pointerTo("Initial purpose"),
		Subscription: Subscribe,
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if created.Description == nil || *created.Description != "Initial purpose" {
		t.Fatalf("created.Description = %v", created.Description)
	}
	if _, err := writer.Post(ctx, root, PostRequest{
		RequestID:   "turn-1:call-1",
		Destination: PostDestination{Kind: "channel", Name: "work"},
		Text:        "hello",
	}); err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	if err := writer.UpsertChannel(ctx, root, UpsertChannelRequest{
		ChannelName: "work",
		Description: "Shared policies",
	}); err != nil {
		t.Fatalf("UpsertChannel(update) error = %v", err)
	}
	updated := localChannel(t, writer, root, "work")
	if updated.Description == nil || *updated.Description != "Shared policies" {
		t.Fatalf("updated.Description = %v", updated.Description)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) || updated.CreatedBy != created.CreatedBy {
		t.Fatalf("upsert replaced channel metadata: %#v", updated)
	}
	if updated.MessageCount != 1 {
		t.Fatalf("upsert changed message count: %#v", updated)
	}

	if err := writer.UpsertChannel(ctx, root, UpsertChannelRequest{
		ChannelName: "fresh",
		Description: "Fresh purpose",
	}); err != nil {
		t.Fatalf("UpsertChannel(create) error = %v", err)
	}
	fresh := localChannel(t, writer, root, "fresh")
	if fresh.Description == nil || *fresh.Description != "Fresh purpose" || fresh.CreatedBy != agent.AgentPathRoot {
		t.Fatalf("fresh = %#v", fresh)
	}

	reader, err := shared.ReadOnly().Open(ctx, host)
	if err != nil {
		t.Fatalf("Open(reader) error = %v", err)
	}
	defer reader.Close()
	err = reader.UpsertChannel(ctx, root, UpsertChannelRequest{ChannelName: "fresh", Description: "Nope"})
	var denied *PermissionDenied
	if !errors.As(err, &denied) {
		t.Fatalf("read-only upsert error = %v", err)
	}
}

func localChannel(t *testing.T, board *LocalAgentMessageBoard, caller string, name string) ChannelSummary {
	t.Helper()
	page, err := board.ListChannels(context.Background(), caller, ChannelQuery{})
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	for _, channel := range page.Results {
		if channel.ChannelName == name {
			return channel
		}
	}
	t.Fatalf("channel %q not listed", name)
	return ChannelSummary{}
}

func TestRemoteBoardRejectsUpsertChannelLikeRust(t *testing.T) {
	board := newTestRemoteBoard(t, "https://host.example/board/", nil)
	err := board.UpsertChannel(context.Background(), "thread-1", UpsertChannelRequest{ChannelName: "work", Description: "Shared"})
	if err == nil || !IsInvalidRequest(err) || !strings.Contains(err.Error(), "remote message boards do not support channel upsert") {
		t.Fatalf("UpsertChannel() error = %v", err)
	}
}

func TestMessageBoardUpsertChannelToolLikeRust(t *testing.T) {
	ctx := context.Background()
	host := newTestHost(t, map[string]agent.AgentPath{"root-thread": agent.AgentPathRoot})
	board := (&InMemoryMessageBoards{}).Open("board-1", host)
	var upsert tool.Executor
	for _, executor := range NewMessageBoardTools(board, "root-thread", agent.AgentPathRoot, "", "") {
		if executor.Spec().Name.Name == "upsert_channel" {
			upsert = executor
		}
	}
	if upsert == nil {
		t.Fatal("upsert_channel tool not registered")
	}
	output, err := upsert.Execute(ctx, &tool.Invocation{
		CallID:  "call-1",
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"channel_name":"ops","description":"Operations"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || !output.Success || output.Body != "null" {
		t.Fatalf("output = %#v", output)
	}
	if _, err := upsert.Execute(ctx, &tool.Invocation{
		CallID:  "call-2",
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"channel_name":"ops"}`},
	}); err == nil {
		t.Fatal("missing description was accepted")
	}
}
