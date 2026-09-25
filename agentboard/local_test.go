package agentboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"codex_go/agent"
	"codex_go/state"
)

// Rust parity: codex-rs/ext/agent-message-board/tests/local_board.rs, SQLite arm.
func TestLocalBoardPersistsPagesAndReadsLikeRust(t *testing.T) {
	host, root, child, childPath := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	board := openLocalBoard(t, sqlite, root, host)

	created, err := board.CreateChannel(context.Background(), root, CreateChannelRequest{ChannelName: "work", Subscription: Subscribe})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if created.ChannelName != "work" || created.CreatedBy != agent.AgentPathRoot || created.MessageCount != 0 || created.LastMessageID != nil {
		t.Fatalf("created = %#v", created)
	}

	request := PostRequest{
		RequestID:   "turn-1:call-1",
		Destination: PostDestination{Kind: "channel", Name: "work"},
		Text:        "hello world",
	}
	posted, err := board.Post(context.Background(), root, request)
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if posted.ChannelName != "work" || posted.Author != agent.AgentPathRoot || posted.ThreadID != posted.MessageID {
		t.Fatalf("posted = %#v", posted)
	}
	if _, err := uuid.Parse(posted.MessageID); err != nil {
		t.Fatalf("message id %q is not a UUID: %v", posted.MessageID, err)
	}
	// A retry with the same request ID returns the same metadata, and a reused
	// ID with different input is rejected.
	retry, err := board.Post(context.Background(), root, request)
	if err != nil || retry.MessageID != posted.MessageID {
		t.Fatalf("Post retry = %#v, %v", retry, err)
	}
	changed := request
	changed.Text = "different"
	if _, err := board.Post(context.Background(), root, changed); err == nil || !IsInvalidRequest(err) {
		t.Fatalf("reused request ID error = %v", err)
	}

	reply, err := board.Post(context.Background(), child, PostRequest{
		RequestID:   "turn-2:call-1",
		Destination: PostDestination{Kind: "thread", ThreadID: posted.MessageID},
		Text:        "second",
	})
	if err != nil {
		t.Fatalf("reply error = %v", err)
	}
	if reply.ThreadID != posted.MessageID || reply.Author != childPath || reply.ChannelName != "work" {
		t.Fatalf("reply = %#v", reply)
	}

	// Threads, previews and reads.
	threads, err := board.ListThreads(context.Background(), root, ThreadQuery{
		ChannelName: "work", Sort: ThreadSortActivity, Direction: NewestFirst,
		Page: PageRequest{Limit: 20}, MaxCharsPerPost: 1_000,
	})
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if len(threads.Results) != 1 {
		t.Fatalf("threads = %#v", threads)
	}
	thread := threads.Results[0]
	if thread.ThreadID != posted.MessageID || thread.ReplyCount != 1 || thread.RootPost.TextPreview != "hello world" {
		t.Fatalf("thread = %#v", thread)
	}
	if thread.LatestReply == nil || thread.LatestReply.TextPreview != "second" || thread.LatestReply.Author != childPath {
		t.Fatalf("latest reply = %#v", thread.LatestReply)
	}
	if !thread.LastActivityAt.Equal(time.Date(2026, 9, 18, 12, 0, 2, 0, time.UTC)) {
		t.Fatalf("last activity = %v", thread.LastActivityAt)
	}

	page, err := board.ReadThread(context.Background(), root, ReadThreadRequest{
		ThreadID: posted.MessageID, Page: PageRequest{Limit: 20}, MaxCharsPerPost: 1_000,
	})
	if err != nil {
		t.Fatalf("ReadThread() error = %v", err)
	}
	if page.RootPost.MessageID != posted.MessageID || len(page.Replies.Results) != 1 || page.Replies.Results[0].TextPreview != "second" {
		t.Fatalf("thread page = %#v", page)
	}

	content, err := board.ReadPost(context.Background(), root, ReadPostRequest{MessageID: posted.MessageID, OffsetChars: 6, LimitChars: 5})
	if err != nil {
		t.Fatalf("ReadPost() error = %v", err)
	}
	if content.Text != "world" || content.NChars != 11 || content.NextOffsetChars != 11 {
		t.Fatalf("content = %#v", content)
	}

	found, err := board.SearchPosts(context.Background(), root, PostQuery{
		Query: pointerTo("SECOND"), Page: PageRequest{Limit: 20}, MaxCharsPerPost: 1_000,
	})
	if err != nil {
		t.Fatalf("SearchPosts() error = %v", err)
	}
	if len(found.Results) != 1 || found.Results[0].MessageID != reply.MessageID {
		t.Fatalf("search = %#v", found)
	}
	byAuthor, err := board.SearchPosts(context.Background(), root, PostQuery{
		Author: pointerTo(childPath), Page: PageRequest{Limit: 20}, MaxCharsPerPost: 1_000,
	})
	if err != nil {
		t.Fatalf("SearchPosts(author) error = %v", err)
	}
	if len(byAuthor.Results) != 1 || byAuthor.Results[0].Author != childPath {
		t.Fatalf("search by author = %#v", byAuthor)
	}
	after, err := board.SearchPosts(context.Background(), root, PostQuery{
		AfterMessageID: pointerTo(posted.MessageID), Page: PageRequest{Limit: 20}, MaxCharsPerPost: 1_000,
	})
	if err != nil {
		t.Fatalf("SearchPosts(after) error = %v", err)
	}
	if len(after.Results) != 1 || after.Results[0].MessageID != reply.MessageID {
		t.Fatalf("search after = %#v", after)
	}

	channels, err := board.ListChannels(context.Background(), root, ChannelQuery{Direction: NewestFirst, Page: PageRequest{Limit: 20}})
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	if len(channels.Results) != 1 || channels.Results[0].MessageCount != 2 || channels.Results[0].LastMessageID == nil || *channels.Results[0].LastMessageID != reply.MessageID {
		t.Fatalf("channels = %#v", channels)
	}

	// Pagination: three more channels, two per page, driven by the cursor.
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, err := board.CreateChannel(context.Background(), root, CreateChannelRequest{ChannelName: name}); err != nil {
			t.Fatalf("CreateChannel(%s) error = %v", name, err)
		}
	}
	first, err := board.ListChannels(context.Background(), root, ChannelQuery{Direction: NewestFirst, Page: PageRequest{Limit: 2}})
	if err != nil {
		t.Fatalf("ListChannels(page 1) error = %v", err)
	}
	if len(first.Results) != 2 || first.NextCursor == nil {
		t.Fatalf("page 1 = %#v", first)
	}
	second, err := board.ListChannels(context.Background(), root, ChannelQuery{Direction: NewestFirst, Page: PageRequest{Limit: 2, Cursor: first.NextCursor}})
	if err != nil {
		t.Fatalf("ListChannels(page 2) error = %v", err)
	}
	if len(second.Results) != 2 || second.NextCursor != nil {
		t.Fatalf("page 2 = %#v", second)
	}
	if _, err := board.ListChannels(context.Background(), root, ChannelQuery{Direction: NewestFirst, Page: PageRequest{Limit: 2, Cursor: pointerTo("not-base64!")}}); err == nil || !IsInvalidRequest(err) {
		t.Fatalf("invalid cursor error = %v", err)
	}

	// Closing the last handle releases the pool; reopening the same database
	// returns the persisted board, including request-id idempotency.
	board.Close()
	reopened := openLocalBoard(t, sqlite, root, host)
	defer reopened.Close()
	persisted, err := reopened.ReadPost(context.Background(), root, ReadPostRequest{MessageID: reply.MessageID, LimitChars: 100})
	if err != nil {
		t.Fatalf("persisted ReadPost() error = %v", err)
	}
	if persisted.Text != "second" {
		t.Fatalf("persisted post = %#v", persisted)
	}
	again, err := reopened.Post(context.Background(), root, request)
	if err != nil || again.MessageID != posted.MessageID {
		t.Fatalf("idempotent Post after reopen = %#v, %v", again, err)
	}
}

func TestLocalBoardSubscriptionsOptOutsAndDeletionLikeRust(t *testing.T) {
	host, root, child, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	board := openLocalBoard(t, sqlite, root, host)
	defer board.Close()
	ctx := context.Background()

	if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "work", Subscription: Subscribe}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	// The root subscribed to the channel, so a child's new thread notifies it.
	if _, err := board.Post(ctx, child, PostRequest{
		RequestID: "child-root", Destination: PostDestination{Kind: "channel", Name: "work"}, Text: "root post",
	}); err != nil {
		t.Fatalf("child post error = %v", err)
	}
	if notifications := host.sent(); len(notifications) != 1 || notifications[0].recipient != root {
		t.Fatalf("channel notification = %#v", notifications)
	}

	// An explicit unsubscribe stops channel notifications until re-subscribed.
	if _, err := board.SetSubscription(ctx, root, SubscriptionRequest{
		Target: SubscriptionTarget{Kind: "channel", ChannelName: "work"}, Change: Unsubscribe,
	}); err != nil {
		t.Fatalf("unsubscribe error = %v", err)
	}
	if _, err := board.Post(ctx, child, PostRequest{
		RequestID: "child-second", Destination: PostDestination{Kind: "channel", Name: "work"}, Text: "quiet",
	}); err != nil {
		t.Fatalf("child post error = %v", err)
	}
	if notifications := host.sent(); len(notifications) != 1 {
		t.Fatalf("notified after unsubscribe: %#v", notifications)
	}
	subscription, err := board.SetSubscription(ctx, root, SubscriptionRequest{
		Target: SubscriptionTarget{Kind: "channel", ChannelName: "work"}, Change: Subscribe,
	})
	if err != nil {
		t.Fatalf("resubscribe error = %v", err)
	}
	if !subscription.Enabled || subscription.TargetAgent != agent.AgentPathRoot || subscription.ThreadID != nil {
		t.Fatalf("subscription state = %#v", subscription)
	}

	// A thread opt-out survives participation: the child's own thread
	// subscription is dropped, and posting into it again must not re-subscribe.
	threadRoot, err := board.Post(ctx, child, PostRequest{
		RequestID: "child-thread", Destination: PostDestination{Kind: "channel", Name: "work"}, Text: "thread",
	})
	if err != nil {
		t.Fatalf("thread post error = %v", err)
	}
	if _, err := board.SetSubscription(ctx, child, SubscriptionRequest{
		Target: SubscriptionTarget{Kind: "thread", ThreadID: threadRoot.MessageID}, Change: Unsubscribe,
	}); err != nil {
		t.Fatalf("thread unsubscribe error = %v", err)
	}
	if _, err := board.Post(ctx, child, PostRequest{
		RequestID: "child-thread-reply", Destination: PostDestination{Kind: "thread", ThreadID: threadRoot.MessageID}, Text: "reply",
	}); err != nil {
		t.Fatalf("thread reply error = %v", err)
	}
	if _, err := board.Post(ctx, root, PostRequest{
		RequestID: "root-thread-reply", Destination: PostDestination{Kind: "thread", ThreadID: threadRoot.MessageID}, Text: "ping",
	}); err != nil {
		t.Fatalf("root thread reply error = %v", err)
	}
	if notifications := host.sent(); len(notifications) != 2 {
		t.Fatalf("opt-out did not prevent the thread notification: %#v", notifications)
	}
	if _, err := board.SetSubscription(ctx, child, SubscriptionRequest{
		Target: SubscriptionTarget{Kind: "thread", ThreadID: threadRoot.MessageID}, Change: Subscribe,
	}); err != nil {
		t.Fatalf("thread resubscribe error = %v", err)
	}
	if _, err := board.Post(ctx, root, PostRequest{
		RequestID: "root-thread-ping", Destination: PostDestination{Kind: "thread", ThreadID: threadRoot.MessageID}, Text: "again",
	}); err != nil {
		t.Fatalf("root thread reply error = %v", err)
	}
	notifications := host.sent()
	if len(notifications) != 3 || notifications[2].recipient != child {
		t.Fatalf("notifications after resubscribe = %#v", notifications)
	}

	// Permanent deletion tombstones the board: writes fail, and the tombstone
	// survives a reopen.
	if err := DeleteLocalBoards(ctx, sqlite, []string{root}); err != nil {
		t.Fatalf("DeleteLocalBoards() error = %v", err)
	}
	if _, err := board.Post(ctx, root, PostRequest{
		RequestID: "after-delete", Destination: PostDestination{Kind: "channel", Name: "work"}, Text: "nope",
	}); err == nil || !strings.Contains(err.Error(), "permanently deleted") {
		t.Fatalf("post after delete error = %v", err)
	}
	if _, err := board.ListChannels(ctx, root, ChannelQuery{Direction: NewestFirst, Page: PageRequest{Limit: 20}}); err != nil {
		t.Fatalf("ListChannels() after delete error = %v", err)
	}
	board.Close()
	reopened := openLocalBoard(t, sqlite, root, host)
	defer reopened.Close()
	if _, err := reopened.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "work"}); err == nil || !strings.Contains(err.Error(), "permanently deleted") {
		t.Fatalf("create after reopen error = %v", err)
	}
	// Another tree's board is untouched.
	other := openLocalBoard(t, sqlite, child, host)
	defer other.Close()
	if _, err := other.CreateChannel(ctx, child, CreateChannelRequest{ChannelName: "other"}); err != nil {
		t.Fatalf("other board CreateChannel() error = %v", err)
	}
}

// The persistence format is Rust's: payload, request and subscription keys must
// stay byte-compatible so a Rust binary can read a Go-written board.
func TestLocalBoardStoresRustCompatibleJSON(t *testing.T) {
	host, root, _, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	board := openLocalBoard(t, sqlite, root, host)
	defer board.Close()
	ctx := context.Background()

	if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "work", Subscription: Subscribe}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	posted, err := board.Post(ctx, root, PostRequest{
		RequestID: "turn-1:call-1", Destination: PostDestination{Kind: "channel", Name: "work"}, Text: "hi",
	})
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(sqlite.Home(), AgentMessageBoardDatabaseFile))
	if err != nil {
		t.Fatalf("sql.Open error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var payload string
	var requestID string
	var request string
	if err := db.QueryRowContext(ctx, "SELECT payload,request_id,request FROM posts WHERE board=? AND id=?", root, posted.MessageID).Scan(&payload, &requestID, &request); err != nil {
		t.Fatalf("read stored post error = %v", err)
	}
	if requestID != root+":turn-1:call-1" {
		t.Fatalf("request_id = %q", requestID)
	}
	if request != `{"request_id":"turn-1:call-1","destination":{"Channel":"work"},"text":"hi","agents_to_notify":[]}` {
		t.Fatalf("request = %s", request)
	}
	var decoded struct {
		Metadata struct {
			MessageID   string `json:"message_id"`
			ChannelName string `json:"channel_name"`
			Author      string `json:"author"`
			ThreadID    string `json:"thread_id"`
			CreatedAt   string `json:"created_at"`
		} `json:"metadata"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("payload is not Rust-shaped JSON: %v (%s)", err, payload)
	}
	if decoded.Text != "hi" || decoded.Metadata.MessageID != posted.MessageID ||
		decoded.Metadata.ChannelName != "work" || decoded.Metadata.Author != "/root" ||
		decoded.Metadata.ThreadID != posted.MessageID {
		t.Fatalf("payload = %s", payload)
	}
	// chrono's serde form for DateTime<Utc> is RFC3339 with a `Z` suffix, while
	// the channels table stores `to_rfc3339()`'s numeric offset.
	if !strings.HasSuffix(decoded.Metadata.CreatedAt, "Z") {
		t.Fatalf("payload created_at = %q, want chrono's serde UTC form", decoded.Metadata.CreatedAt)
	}
	var channelCreatedAt string
	if err := db.QueryRowContext(ctx, "SELECT created_at FROM channels WHERE board=? AND name=?", root, "work").Scan(&channelCreatedAt); err != nil {
		t.Fatalf("read channel created_at error = %v", err)
	}
	if !strings.HasSuffix(channelCreatedAt, "+00:00") {
		t.Fatalf("channel created_at = %q, want chrono's to_rfc3339 offset form", channelCreatedAt)
	}

	// Posting subscribed the author to the channel and to its own thread, so both
	// externally tagged target forms are stored.
	rows, err := db.QueryContext(ctx, "SELECT target FROM subscriptions WHERE board=? AND agent=? ORDER BY target", root, root)
	if err != nil {
		t.Fatalf("read subscription targets error = %v", err)
	}
	targets := []string{}
	for rows.Next() {
		target := ""
		if err := rows.Scan(&target); err != nil {
			t.Fatalf("scan subscription target error = %v", err)
		}
		targets = append(targets, target)
	}
	_ = rows.Close()
	if len(targets) != 2 || targets[0] != `{"Channel":"work"}` ||
		targets[1] != `{"Thread":"`+posted.MessageID+`"}` {
		t.Fatalf("subscription targets = %#v", targets)
	}
	var nameSearch string
	if err := db.QueryRowContext(ctx, "SELECT name_search FROM channels WHERE board=? AND name=?", root, "work").Scan(&nameSearch); err != nil {
		t.Fatalf("read channel search key error = %v", err)
	}
	if nameSearch != "work" {
		t.Fatalf("name_search = %q", nameSearch)
	}
	var bodySearch string
	if err := db.QueryRowContext(ctx, "SELECT body_search FROM posts WHERE board=? AND id=?", root, posted.MessageID).Scan(&bodySearch); err != nil {
		t.Fatalf("read body search key error = %v", err)
	}
	if bodySearch != "hi" {
		t.Fatalf("body_search = %q", bodySearch)
	}
}

// The collaboration tools are backend-agnostic: the same nine tools run over the
// SQLite board (Rust's local_board.rs exercises the tool layer over both arms).
func TestMessageBoardToolsRunOverTheLocalBoardLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	board := openLocalBoard(t, sqlite, root, host)
	defer board.Close()
	execs := boardToolSet(t, board, root, agent.AgentPathRoot)

	if _, err := boardToolBody(t, execs["create_channel"], boardInvocation(t, "create_channel", map[string]any{"channel_name": "work"})); err != nil {
		t.Fatalf("create_channel error = %v", err)
	}
	posted, err := boardToolBody(t, execs["post"], boardInvocation(t, "post", map[string]any{"channel_name": "work", "text": "hello"}))
	if err != nil {
		t.Fatalf("post error = %v", err)
	}
	rootID, _ := posted["message_id"].(string)
	if rootID == "" {
		t.Fatalf("post body = %#v", posted)
	}
	if _, err := boardToolBody(t, execs["post"], boardInvocation(t, "post", map[string]any{"thread_id": rootID, "text": "reply"})); err != nil {
		t.Fatalf("reply error = %v", err)
	}
	threadPage, err := boardToolBody(t, execs["read_thread"], boardInvocation(t, "read_thread", map[string]any{"thread_id": rootID}))
	if err != nil {
		t.Fatalf("read_thread error = %v", err)
	}
	replies, _ := threadPage["replies"].(map[string]any)
	if replies["n_returned"] != float64(1) {
		t.Fatalf("replies = %#v", replies)
	}
	page, err := boardToolBody(t, execs["get_channels"], boardInvocation(t, "get_channels", map[string]any{}))
	if err != nil {
		t.Fatalf("get_channels error = %v", err)
	}
	if page["n_returned"] != float64(1) {
		t.Fatalf("channels = %#v", page)
	}
}

func openBoardSqliteConfig(t *testing.T) state.SqliteConfig {
	t.Helper()
	sqlite, err := state.NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatalf("NewSqliteConfig() error = %v", err)
	}
	return sqlite
}

func openLocalBoard(t *testing.T, sqlite state.SqliteConfig, identity string, host Host) *LocalAgentMessageBoard {
	t.Helper()
	board, err := OpenLocalBoard(context.Background(), sqlite, identity, host)
	if err != nil {
		t.Fatalf("OpenLocalBoard() error = %v", err)
	}
	return board
}

func pointerTo[T any](value T) *T {
	return &value
}
