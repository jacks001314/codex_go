package agentboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"codex_go/agent"
)

// Rust parity: codex-rs/message-board/tests/local_board.rs (#56461). Appends
// share the write path but never subscribe their author or notify anyone.

func TestInMemoryBoardAppendIsSilentLikeRust(t *testing.T) {
	ctx := context.Background()
	host, root, child, childPath := testTree(t)
	boards := &InMemoryMessageBoards{}
	board := boards.Open(root, host)
	defer board.Close()

	request := AppendRequest{
		RequestID:   "evidence-1",
		Destination: PostDestination{Kind: "new_channel", Name: "evidence"},
		Text:        "Permission revoked.",
	}
	appended, err := board.Append(ctx, root, request)
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if appended.Author != agent.AgentPathRoot || appended.ChannelName != "evidence" || appended.ThreadID != appended.MessageID {
		t.Fatalf("appended = %#v", appended)
	}
	retry, err := board.Append(ctx, root, request)
	if err != nil || *retry != *appended {
		t.Fatalf("Append retry = %#v, %v, want the original metadata", retry, err)
	}
	if sent := host.sent(); len(sent) != 0 {
		t.Fatalf("notifications = %#v, want none for a silent append", sent)
	}
	state := board.ref.state
	state.mu.Lock()
	subscriptions := len(state.subscriptions)
	state.mu.Unlock()
	if subscriptions != 0 {
		t.Fatalf("subscriptions = %d, want none for a silent append", subscriptions)
	}
	content, err := board.ReadPost(ctx, child, ReadPostRequest{MessageID: appended.MessageID, LimitChars: 100})
	if err != nil {
		t.Fatalf("ReadPost() error = %v", err)
	}
	if content.Text != request.Text || content.NChars != len([]rune(request.Text)) {
		t.Fatalf("content = %#v", content)
	}

	// A reused request ID must match both the payload and the operation.
	changed := request
	changed.Text = "A different decision"
	if _, err := board.Append(ctx, root, changed); err == nil || !IsInvalidRequest(err) {
		t.Fatalf("changed append error = %v, want an invalid-request rejection", err)
	}
	if _, err := board.Post(ctx, root, PostRequest{
		RequestID:   request.RequestID,
		Destination: request.Destination,
		Text:        request.Text,
	}); err == nil || !IsInvalidRequest(err) {
		t.Fatalf("post reusing an append ID error = %v, want an invalid-request rejection", err)
	}

	// Discussion posts on the same board still notify and subscribe the author.
	posted, err := board.Post(ctx, root, PostRequest{
		RequestID:      "discussion-1",
		Destination:    PostDestination{Kind: "channel", Name: "evidence"},
		Text:           "Discuss the evidence.",
		AgentsToNotify: []agent.AgentPath{childPath},
	})
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	sent := host.sent()
	if len(sent) != 1 || sent[0].recipient != child || sent[0].metadata != *posted {
		t.Fatalf("notifications = %#v, want one delivery to the child", sent)
	}
	state.mu.Lock()
	threadSubscribed := false
	for target := range state.subscriptions {
		if target.Kind == "thread" && target.ThreadID == posted.ThreadID {
			threadSubscribed = true
		}
	}
	state.mu.Unlock()
	if !threadSubscribed {
		t.Fatal("a discussion post did not subscribe its author to the thread")
	}
}

func TestLocalBoardAppendPersistsEvidenceWithoutSideEffectsLikeRust(t *testing.T) {
	ctx := context.Background()
	host, root, child, _ := testTree(t)
	shared := NewLocalBoard(openBoardSqliteConfig(t), root)
	writer, err := shared.ReadWrite().Open(ctx, host)
	if err != nil {
		t.Fatalf("Open(writer) error = %v", err)
	}
	defer writer.Close()
	reader, err := shared.ReadOnly().Open(ctx, host)
	if err != nil {
		t.Fatalf("Open(reader) error = %v", err)
	}
	defer reader.Close()

	// Include characters requiring JSON escaping in the persisted target.
	channel := "audit\"\\é"
	request := AppendRequest{
		RequestID:   "original",
		Destination: PostDestination{Kind: "new_channel", Name: channel},
		Text:        "Deployment was revoked.",
	}
	original, err := writer.Append(ctx, root, request)
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	retry, err := writer.Append(ctx, root, request)
	if err != nil || *retry != *original {
		t.Fatalf("Append retry = %#v, %v, want the original metadata", retry, err)
	}

	// A read-only connection keeps the write permission check.
	_, err = reader.Append(ctx, child, AppendRequest{
		RequestID:   "reader",
		Destination: PostDestination{Kind: "channel", Name: channel},
		Text:        request.Text,
	})
	var denied *PermissionDenied
	if !errors.As(err, &denied) {
		t.Fatalf("read-only append error = %v, want a permission denial", err)
	}

	// A reused request ID must match both the payload and the operation.
	changed := request
	changed.Text = "A different decision"
	if _, err := writer.Append(ctx, root, changed); err == nil || !IsInvalidRequest(err) {
		t.Fatalf("changed append error = %v, want an invalid-request rejection", err)
	}
	content, err := reader.ReadPost(ctx, child, ReadPostRequest{MessageID: original.MessageID, LimitChars: 100})
	if err != nil {
		t.Fatalf("ReadPost() error = %v", err)
	}
	if content.Text != request.Text {
		t.Fatalf("content.Text = %q", content.Text)
	}
	if sent := host.sent(); len(sent) != 0 {
		t.Fatalf("notifications = %#v, want none for a silent append", sent)
	}
	if count := localSubscriptionCount(t, writer, root); count != 0 {
		t.Fatalf("subscriptions after append = %d, want none", count)
	}

	// The durable write survives reopening and still answers retries.
	writer.Close()
	reopened, err := shared.ReadWrite().Open(ctx, host)
	if err != nil {
		t.Fatalf("Open(reopened) error = %v", err)
	}
	defer reopened.Close()
	replayed, err := reopened.Append(ctx, root, request)
	if err != nil || *replayed != *original {
		t.Fatalf("Append after reopen = %#v, %v, want the original metadata", replayed, err)
	}

	// Discussion posts on the same board still subscribe their author and
	// notify once; an append cannot reuse the post's request ID.
	posted, err := reopened.Post(ctx, root, PostRequest{
		RequestID:   "discussion",
		Destination: PostDestination{Kind: "channel", Name: channel},
		Text:        "Discuss the latest evidence.",
	})
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if posted.ChannelName != channel {
		t.Fatalf("posted.ChannelName = %q, want %q", posted.ChannelName, channel)
	}
	if count := localSubscriptionCount(t, reopened, root); count != 1 {
		t.Fatalf("subscriptions after post = %d, want the thread subscription", count)
	}
	if _, err := reopened.Append(ctx, root, AppendRequest{
		RequestID:   "discussion",
		Destination: PostDestination{Kind: "channel", Name: channel},
		Text:        "Discuss the latest evidence.",
	}); err == nil || !IsInvalidRequest(err) {
		t.Fatalf("append reusing a post ID error = %v, want an invalid-request rejection", err)
	}
}

func localSubscriptionCount(t *testing.T, board *LocalAgentMessageBoard, caller string) int {
	t.Helper()
	var count int
	if err := board.db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM subscriptions WHERE board=? AND agent=?",
		board.identity, caller,
	).Scan(&count); err != nil {
		t.Fatalf("subscription count query error = %v", err)
	}
	return count
}

// Rust's remote client rejects appends locally: the protocol has no silent
// write operation and posting is not equivalent.
type appendRejectTransport struct {
	calls int
}

func (c *appendRejectTransport) Do(*http.Request) (*http.Response, error) {
	c.calls++
	return nil, errors.New("unexpected request")
}

func TestRemoteBoardRejectsAppendLikeRust(t *testing.T) {
	transport := &appendRejectTransport{}
	board, err := NewRemoteBoard(RemoteBoardOptions{
		HTTP:     transport,
		Endpoint: "https://host.example/board/",
		Board:    "session-1",
		Token:    remoteTestToken,
	})
	if err != nil {
		t.Fatalf("NewRemoteBoard() error = %v", err)
	}
	_, err = board.Append(context.Background(), "thread-1", AppendRequest{
		RequestID:   "call-1",
		Destination: PostDestination{Kind: "channel", Name: "work"},
		Text:        "Permission revoked.",
	})
	if err == nil || !IsInvalidRequest(err) || !strings.Contains(err.Error(), "remote message boards do not support silent append") {
		t.Fatalf("Append() error = %v", err)
	}
	if transport.calls != 0 {
		t.Fatalf("append issued %d HTTP requests, want none", transport.calls)
	}
}

// Consumers select the tools they expose, in the supplied order (Rust #55973).
func TestMessageBoardToolSelectionLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	boards := &InMemoryMessageBoards{}
	board := boards.Open(root, host)
	defer board.Close()
	selected := []MessageBoardTool{MessageBoardToolReadThread, MessageBoardToolReadPost}
	executors := NewMessageBoardToolsWithSelection(board, selected, root, agent.AgentPathRoot, "", "", nil)
	if len(executors) != len(selected) {
		t.Fatalf("selected %d tools, want %d", len(executors), len(selected))
	}
	for index, executor := range executors {
		if got := executor.Spec().Name.Name; got != string(selected[index]) {
			t.Fatalf("tool %d = %q, want %q", index, got, selected[index])
		}
	}
	if _, err := board.CreateChannel(context.Background(), root, CreateChannelRequest{ChannelName: "work", Subscription: Subscribe}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	posted, err := board.Post(context.Background(), root, PostRequest{
		RequestID:   "call-1",
		Destination: PostDestination{Kind: "channel", Name: "work"},
		Text:        "Evidence.",
	})
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	threadPage, err := boardToolBody(t, boardToolExecutor(t, executors, "read_thread"), boardInvocation(t, "read_thread", map[string]any{"thread_id": posted.MessageID}))
	if err != nil {
		t.Fatalf("read_thread error = %v", err)
	}
	rootPost, _ := threadPage["root_post"].(map[string]any)
	if rootPost["message_id"] != posted.MessageID {
		t.Fatalf("read_thread body = %#v", threadPage)
	}
}

// The append tool is only exposed to consumers that select it (Rust #56461).
func TestMessageBoardAppendToolLikeRust(t *testing.T) {
	ctx := context.Background()
	host, root, _, _ := testTree(t)
	boards := &InMemoryMessageBoards{}
	board := boards.Open(root, host)
	defer board.Close()
	executors := NewMessageBoardToolsWithSelection(board, []MessageBoardTool{MessageBoardToolAppend, MessageBoardToolReadPost}, root, agent.AgentPathRoot, "", "", nil)
	appendTool := boardToolExecutor(t, executors, "append")

	spec := appendTool.Spec()
	if spec.Description != messageBoardSpecFor("append").description {
		t.Fatalf("append description = %q", spec.Description)
	}
	newChannelField, _ := spec.InputSchema["properties"].(map[string]any)["new_channel_name"].(map[string]any)
	if newChannelField["description"] != "Create this channel without subscribing anyone." {
		t.Fatalf("new_channel_name field = %#v", newChannelField)
	}

	invocation := boardInvocation(t, "append", map[string]any{"new_channel_name": "evidence", "text": "Permission revoked."})
	appended, err := boardToolBody(t, appendTool, invocation)
	if err != nil {
		t.Fatalf("append error = %v", err)
	}
	retried, err := boardToolBody(t, appendTool, invocation)
	if err != nil {
		t.Fatalf("append retry error = %v", err)
	}
	if retried["message_id"] != appended["message_id"] {
		t.Fatalf("append retry = %#v, want the original metadata", retried)
	}
	read, err := boardToolBody(t, boardToolExecutor(t, executors, "read_post"), boardInvocation(t, "read_post", map[string]any{"message_id": appended["message_id"]}))
	if err != nil {
		t.Fatalf("read_post error = %v", err)
	}
	if read["text"] != "Permission revoked." {
		t.Fatalf("read_post body = %#v", read)
	}

	for _, args := range []map[string]any{
		{"text": "no destination"},
		{"text": "invalid recipient", "new_channel_name": "evidence", "agents_to_notify": []string{"worker"}},
		{"text": "two destinations", "channel_name": "a", "new_channel_name": "b"},
	} {
		output, err := appendTool.Execute(ctx, boardInvocation(t, "append", args))
		if err == nil {
			t.Fatalf("append(%v) = %#v, want a model error", args, output)
		}
	}
}
