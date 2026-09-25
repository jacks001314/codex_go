package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/agent"
)

// testHost mirrors the shared test host in Rust's local_board.rs: a fixed
// membership map, a clock that advances one second per read, and a switchable
// notification sink.
type testHost struct {
	mu            sync.Mutex
	members       map[string]agent.AgentPath
	clock         time.Time
	agentPathCall int
	active        bool
	failNotify    bool
	notifications []notificationRecord
}

type notificationRecord struct {
	recipient string
	metadata  PostMetadata
}

func newTestHost(t *testing.T, members map[string]agent.AgentPath) *testHost {
	t.Helper()
	return &testHost{
		members: members,
		clock:   time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC),
		active:  true,
	}
}

func (h *testHost) AgentPath(_ context.Context, caller string) (agent.AgentPath, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agentPathCall++
	path, ok := h.members[caller]
	if !ok {
		return "", invalid("unknown agent")
	}
	return path, nil
}

func (h *testHost) ResolveAgent(_ context.Context, path agent.AgentPath) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, member := range h.members {
		if member == path {
			return id, nil
		}
	}
	return "", invalid("unknown agent")
}

func (h *testHost) CurrentTime(_ context.Context, _ string) (time.Time, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.clock
	h.clock = h.clock.Add(time.Second)
	return now, nil
}

func (h *testHost) Notify(_ context.Context, recipient string, post PostPreview) (NotificationDelivery, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failNotify {
		return "", errors.New("notification transport failed")
	}
	if !h.active {
		return NotificationSkippedInactive, nil
	}
	h.notifications = append(h.notifications, notificationRecord{recipient: recipient, metadata: post.PostMetadata})
	return NotificationAccepted, nil
}

func (h *testHost) sent() []notificationRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]notificationRecord(nil), h.notifications...)
}

func (h *testHost) setActive(active bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active = active
}

func (h *testHost) setFailNotify(fail bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failNotify = fail
}

func testTree(t *testing.T) (*testHost, string, string, agent.AgentPath) {
	t.Helper()
	root := "root-thread"
	child := "child-thread"
	childPath := agent.AgentPath("/root/worker")
	host := newTestHost(t, map[string]agent.AgentPath{
		root:  agent.AgentPath("/root"),
		child: childPath,
	})
	return host, root, child, childPath
}

// Rust parity: in_memory_tests.rs
// opening_boards_prunes_expired_entries_and_preserves_live_state plus the
// in-memory arm of local_board.rs's shared-handle scenarios.
func TestInMemoryBoardsShareStateAndReleaseItLikeRust(t *testing.T) {
	host, root, child, childPath := testTree(t)
	boards := &InMemoryMessageBoards{}
	live := boards.Open(root, host)
	shared := boards.Open(root, host)
	if boards.Len() != 1 {
		t.Fatalf("registry size = %d, want one shared board", boards.Len())
	}
	request := PostRequest{
		RequestID:      "same-call",
		Destination:    PostDestination{Kind: "new_channel", Name: "work"},
		Text:           "done",
		AgentsToNotify: []agent.AgentPath{childPath},
	}
	first, err := live.Post(context.Background(), root, request)
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	retry, err := shared.Post(context.Background(), root, request)
	if err != nil {
		t.Fatalf("retry Post() error = %v", err)
	}
	if *retry != *first {
		t.Fatalf("retry metadata = %#v, want %#v", retry, first)
	}
	sent := host.sent()
	if len(sent) != 1 || sent[0].recipient != child || sent[0].metadata != *first {
		t.Fatalf("notifications = %#v, want one delivery to the child", sent)
	}
	read, err := shared.ReadPost(context.Background(), child, ReadPostRequest{
		MessageID: first.MessageID, LimitChars: 20,
	})
	if err != nil {
		t.Fatalf("ReadPost() error = %v", err)
	}
	if read.Text != "done" || read.NChars != 4 || read.NextOffsetChars != 4 {
		t.Fatalf("read = %#v", read)
	}

	// Another identity shares nothing.
	other := boards.Open("other-session", host)
	if _, err := other.ReadPost(context.Background(), root, ReadPostRequest{MessageID: first.MessageID, LimitChars: 5}); err == nil {
		t.Fatal("a board for another identity saw the post")
	}
	if boards.Len() != 2 {
		t.Fatalf("registry size = %d, want two boards", boards.Len())
	}

	// Releasing every handle discards the state.
	live.Close()
	shared.Close()
	other.Close()
	if boards.Len() != 0 {
		t.Fatalf("registry size = %d after release, want 0", boards.Len())
	}
	fresh := boards.Open(root, host)
	defer fresh.Close()
	if _, err := fresh.ReadPost(context.Background(), root, ReadPostRequest{MessageID: first.MessageID, LimitChars: 5}); err == nil {
		t.Fatal("released board state survived into a fresh handle")
	}
}

// Rust parity: local_board.rs shared_handles_resume_posts_and_preserve_subscription_rules
// (the in-memory contract): posting subscribes the author to the thread, the
// channel subscription delivers new roots, and read_post counts characters.
func TestInMemoryPostSubscriptionAndUnicodeReadsLikeRust(t *testing.T) {
	host, root, child, childPath := testTree(t)
	boards := &InMemoryMessageBoards{}
	first := boards.Open(root, host)
	defer first.Close()
	if _, err := first.Post(context.Background(), child, PostRequest{
		RequestID:   "create-proofs",
		Destination: PostDestination{Kind: "new_channel", Name: "proofs"},
		Text:        "Share proofs here.",
	}); err != nil {
		t.Fatalf("create channel Post() error = %v", err)
	}
	request := PostRequest{
		RequestID:      "call-1",
		Destination:    PostDestination{Kind: "channel", Name: "proofs"},
		Text:           "é🦀 proof",
		AgentsToNotify: []agent.AgentPath{agent.AgentPath("/root")},
	}
	second := boards.Open(root, host)
	defer second.Close()
	var (
		metadata  *PostMetadata
		duplicate *PostMetadata
		errFirst  error
		errSecond error
		wait      sync.WaitGroup
	)
	wait.Add(2)
	go func() {
		defer wait.Done()
		metadata, errFirst = first.Post(context.Background(), root, request)
	}()
	go func() {
		defer wait.Done()
		duplicate, errSecond = second.Post(context.Background(), root, request)
	}()
	wait.Wait()
	if errFirst != nil || errSecond != nil {
		t.Fatalf("concurrent Post() errors = %v, %v", errFirst, errSecond)
	}
	if *metadata != *duplicate {
		t.Fatalf("duplicate metadata = %#v, want %#v", duplicate, metadata)
	}
	// The creator (child) is subscribed to the channel and receives the post;
	// the explicitly named root author is the poster twice over and is excluded.
	sent := host.sent()
	if len(sent) != 1 || sent[0].recipient != child {
		t.Fatalf("notifications = %#v, want only the channel subscriber", sent)
	}
	content, err := second.ReadPost(context.Background(), child, ReadPostRequest{
		MessageID: metadata.MessageID, OffsetChars: 1, LimitChars: 2,
	})
	if err != nil {
		t.Fatalf("ReadPost() error = %v", err)
	}
	if content.Text != "🦀 " || content.NChars != len([]rune("é🦀 proof")) || content.NextOffsetChars != 3 {
		t.Fatalf("content = %#v", content)
	}

	// An explicit agent target may change another member's subscription.
	state, err := second.SetSubscription(context.Background(), root, SubscriptionRequest{
		Target:      SubscriptionTarget{Kind: "channel", ChannelName: "proofs"},
		TargetAgent: &childPath,
		Change:      Unsubscribe,
	})
	if err != nil {
		t.Fatalf("SetSubscription() error = %v", err)
	}
	if state.Enabled || state.TargetAgent != childPath || state.ChannelName != "proofs" || state.ThreadID != nil {
		t.Fatalf("subscription state = %#v", state)
	}
	if state.LastMessageID == nil || *state.LastMessageID != metadata.MessageID {
		t.Fatalf("last message id = %#v", state.LastMessageID)
	}
}

// Rust parity: local_board.rs
// failed_requests_do_not_create_channels_or_notify_inactive_agents: recipient
// resolution happens before any mutation, inactive agents are skipped, and a
// failed notification never fails a committed post.
func TestInMemoryFailedRequestsAndInactiveAgentsLikeRust(t *testing.T) {
	host, root, _, childPath := testTree(t)
	host.setActive(false)
	boards := &InMemoryMessageBoards{}
	board := boards.Open(root, host)
	defer board.Close()

	request := PostRequest{
		RequestID:      "post",
		Destination:    PostDestination{Kind: "new_channel", Name: "work"},
		Text:           "first",
		AgentsToNotify: []agent.AgentPath{agent.AgentPath("/root/unknown")},
	}
	if _, err := board.Post(context.Background(), root, request); err == nil {
		t.Fatal("Post() with an unknown recipient succeeded")
	}
	// The failed request must not have created the channel.
	page, err := board.ListChannels(context.Background(), root, ChannelQuery{})
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	if len(page.Results) != 0 {
		t.Fatalf("channels = %#v, want none after a failed request", page.Results)
	}

	request.AgentsToNotify = []agent.AgentPath{childPath}
	posted, err := board.Post(context.Background(), root, request)
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if len(host.sent()) != 0 {
		t.Fatalf("inactive agent received a notification: %#v", host.sent())
	}
	host.setActive(true)
	retried, err := board.Post(context.Background(), root, request)
	if err != nil || *retried != *posted {
		t.Fatalf("retry = %#v, %v", retried, err)
	}
	if len(host.sent()) != 0 {
		t.Fatalf("a replayed post notified again: %#v", host.sent())
	}
	// The same request ID with different input is rejected.
	changed := request
	changed.Text = "different"
	if _, err := board.Post(context.Background(), root, changed); err == nil {
		t.Fatal("a reused request ID with different input was accepted")
	}

	// A notification failure leaves the post committed.
	host.setFailNotify(true)
	reply := PostRequest{
		RequestID:      "reply",
		Destination:    PostDestination{Kind: "thread", ThreadID: posted.ThreadID},
		Text:           "saved even if the notice fails",
		AgentsToNotify: []agent.AgentPath{childPath},
	}
	saved, err := board.Post(context.Background(), root, reply)
	if err != nil {
		t.Fatalf("Post() with a failing notification error = %v", err)
	}
	host.setFailNotify(false)
	if replay, err := board.Post(context.Background(), root, reply); err != nil || *replay != *saved {
		t.Fatalf("replay = %#v, %v", replay, err)
	}
	content, err := board.ReadPost(context.Background(), root, ReadPostRequest{MessageID: saved.MessageID, LimitChars: 100})
	if err != nil {
		t.Fatalf("ReadPost() error = %v", err)
	}
	if content.Text != "saved even if the notice fails" || content.NChars != 30 || content.NextOffsetChars != 30 {
		t.Fatalf("content = %#v", content)
	}
}

// Rust parity: local_board.rs queries_enforce_page_and_preview_caps: the page
// cap is 50, the preview budget is 20k characters split across the page, and
// search folds Unicode case (Straße matches STRASSE).
func TestInMemoryQueriesEnforcePageAndPreviewCapsLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	boards := &InMemoryMessageBoards{}
	board := boards.Open(root, host)
	defer board.Close()
	host.setActive(false)
	if _, err := board.CreateChannel(context.Background(), root, CreateChannelRequest{
		ChannelName: "Straße", Subscription: Unsubscribe,
	}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	text := "Straße" + strings.Repeat("x", 1000)
	for index := 0; index < 51; index++ {
		if _, err := board.Post(context.Background(), root, PostRequest{
			RequestID:   fmt.Sprintf("%d", index),
			Destination: PostDestination{Kind: "channel", Name: "Straße"},
			Text:        text,
		}); err != nil {
			t.Fatalf("Post(%d) error = %v", index, err)
		}
	}
	search := "STRASSE"
	channels, err := board.ListChannels(context.Background(), root, ChannelQuery{
		Query: &search, Direction: NewestFirst,
	})
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	if len(channels.Results) != 1 || channels.Results[0].ChannelName != "Straße" {
		t.Fatalf("channel search = %#v", channels.Results)
	}
	query := PostQuery{Query: &search, Page: PageRequest{Limit: 1 << 30}, MaxCharsPerPost: 1 << 30}
	page, err := board.SearchPosts(context.Background(), root, query)
	if err != nil {
		t.Fatalf("SearchPosts() error = %v", err)
	}
	if len(page.Results) != MaxPageLimit {
		t.Fatalf("page size = %d, want %d", len(page.Results), MaxPageLimit)
	}
	total := 0
	for _, post := range page.Results {
		total += len([]rune(post.TextPreview))
		if !post.Truncated {
			t.Fatalf("preview was not truncated: %#v", post)
		}
	}
	if total != outputBudgetChars {
		t.Fatalf("preview budget = %d, want %d", total, outputBudgetChars)
	}
	last, err := board.SearchPosts(context.Background(), root, PostQuery{
		Query: &search, Page: PageRequest{Cursor: page.NextCursor, Limit: 1 << 30}, MaxCharsPerPost: 1 << 30,
	})
	if err != nil {
		t.Fatalf("SearchPosts(next) error = %v", err)
	}
	if len(last.Results) != 1 || last.NextCursor != nil {
		t.Fatalf("last page = %#v", last)
	}
	if _, err := board.SearchPosts(context.Background(), root, PostQuery{
		Page: PageRequest{Cursor: stringPointer("not-a-cursor")},
	}); err == nil {
		t.Fatal("an invalid cursor was accepted")
	}
}

// Rust parity: local_board.rs queries_page_discussions_and_search_unicode: the
// thread page carries the root post plus newest replies, search intersects
// channel/author/after filters, and read_thread rejects a reply id.
func TestInMemoryQueriesPageDiscussionsAndSearchUnicodeLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	boards := &InMemoryMessageBoards{}
	board := boards.Open(root, host)
	defer board.Close()
	first, err := board.Post(context.Background(), root, PostRequest{
		RequestID:   "first",
		Destination: PostDestination{Kind: "new_channel", Name: "work"},
		Text:        "Éclair",
	})
	if err != nil {
		t.Fatalf("first Post() error = %v", err)
	}
	second, err := board.Post(context.Background(), root, PostRequest{
		RequestID:   "second",
		Destination: PostDestination{Kind: "channel", Name: "work"},
		Text:        "second",
	})
	if err != nil {
		t.Fatalf("second Post() error = %v", err)
	}
	reply, err := board.Post(context.Background(), root, PostRequest{
		RequestID:   "reply",
		Destination: PostDestination{Kind: "thread", ThreadID: first.MessageID},
		Text:        "Éclair reply",
	})
	if err != nil {
		t.Fatalf("reply Post() error = %v", err)
	}

	channels, err := board.ListChannels(context.Background(), root, ChannelQuery{
		Query: stringPointer("WORK"), Direction: NewestFirst,
	})
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	if len(channels.Results) != 1 || channels.Results[0].MessageCount != 3 ||
		channels.Results[0].LastMessageID == nil || *channels.Results[0].LastMessageID != reply.MessageID {
		t.Fatalf("channel summary = %#v", channels.Results)
	}

	one := 1
	author := agent.AgentPath("/root")
	search, err := board.SearchPosts(context.Background(), root, PostQuery{
		ChannelName:     stringPointer("work"),
		Query:           stringPointer("éclair"),
		AfterMessageID:  &first.MessageID,
		Author:          &author,
		MaxCharsPerPost: one,
	})
	if err != nil {
		t.Fatalf("SearchPosts() error = %v", err)
	}
	if len(search.Results) != 1 || search.Results[0].MessageID != reply.MessageID ||
		search.Results[0].TextPreview != "É" || search.Results[0].NChars != 12 || !search.Results[0].Truncated {
		t.Fatalf("search = %#v", search.Results)
	}

	thread, err := board.ReadThread(context.Background(), root, ReadThreadRequest{
		ThreadID: first.MessageID, MaxCharsPerPost: one,
	})
	if err != nil {
		t.Fatalf("ReadThread() error = %v", err)
	}
	if thread.RootPost.MessageID != first.MessageID || thread.RootPost.TextPreview != "É" ||
		thread.RootPost.NChars != 6 || !thread.RootPost.Truncated {
		t.Fatalf("thread root = %#v", thread.RootPost)
	}
	if len(thread.Replies.Results) != 1 || thread.Replies.Results[0].MessageID != reply.MessageID {
		t.Fatalf("thread replies = %#v", thread.Replies.Results)
	}
	encoded, err := json.Marshal(thread)
	if err != nil {
		t.Fatalf("Marshal(thread) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal(thread) error = %v", err)
	}
	// Rust flattens the reply page into the thread page (serde `flatten`), so the
	// results and their metadata sit beside `root_post`.
	if _, nested := decoded["replies"]; nested {
		t.Fatalf("thread page kept a nested replies field: %#v", decoded)
	}
	if decoded["n_returned"] != float64(1) || decoded["has_more"] != false {
		t.Fatalf("thread page metadata = %#v", decoded)
	}
	results, ok := decoded["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("thread replies JSON = %#v", decoded["results"])
	}

	if _, err := board.ReadThread(context.Background(), root, ReadThreadRequest{
		ThreadID: reply.MessageID, MaxCharsPerPost: one,
	}); err == nil {
		t.Fatal("read_thread accepted a reply id as a thread id")
	}
	if _, err := board.ListChannels(context.Background(), "unknown-thread", ChannelQuery{}); err == nil {
		t.Fatal("a non-member caller was accepted")
	}
	// A post to an existing channel starts a new top-level thread of its own.
	if second.ThreadID != second.MessageID {
		t.Fatalf("second post thread = %q, want its own root", second.ThreadID)
	}
}

// Rust parity: local_board.rs thread listing sorts by creation or activity.
func TestInMemoryThreadListingSortsLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	boards := &InMemoryMessageBoards{}
	board := boards.Open(root, host)
	defer board.Close()
	first, err := board.Post(context.Background(), root, PostRequest{
		RequestID: "a", Destination: PostDestination{Kind: "new_channel", Name: "work"}, Text: "first",
	})
	if err != nil {
		t.Fatalf("first Post() error = %v", err)
	}
	second, err := board.Post(context.Background(), root, PostRequest{
		RequestID: "b", Destination: PostDestination{Kind: "channel", Name: "work"}, Text: "second",
	})
	if err != nil {
		t.Fatalf("second Post() error = %v", err)
	}
	// Reply to the first thread so activity ordering differs from creation.
	if _, err := board.Post(context.Background(), root, PostRequest{
		RequestID: "c", Destination: PostDestination{Kind: "thread", ThreadID: first.MessageID}, Text: "reply",
	}); err != nil {
		t.Fatalf("reply Post() error = %v", err)
	}
	created, err := board.ListThreads(context.Background(), root, ThreadQuery{
		ChannelName: "work", Sort: ThreadSortCreated, Direction: NewestFirst,
	})
	if err != nil {
		t.Fatalf("ListThreads(created) error = %v", err)
	}
	if len(created.Results) != 2 || created.Results[0].ThreadID != second.MessageID {
		t.Fatalf("created order = %#v", created.Results)
	}
	activity, err := board.ListThreads(context.Background(), root, ThreadQuery{
		ChannelName: "work", Sort: ThreadSortActivity, Direction: NewestFirst,
	})
	if err != nil {
		t.Fatalf("ListThreads(activity) error = %v", err)
	}
	if len(activity.Results) != 2 || activity.Results[0].ThreadID != first.MessageID {
		t.Fatalf("activity order = %#v", activity.Results)
	}
	if activity.Results[0].ReplyCount != 1 || activity.Results[0].LatestReply == nil ||
		activity.Results[0].LatestReply.MessageID == "" {
		t.Fatalf("thread summary = %#v", activity.Results[0])
	}
}

// Rust parity: local_board.rs board validation limits.
func TestInMemoryValidationLimitsLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	boards := &InMemoryMessageBoards{}
	board := boards.Open(root, host)
	defer board.Close()
	for _, testCase := range []struct {
		name    string
		request CreateChannelRequest
	}{
		{name: "empty", request: CreateChannelRequest{ChannelName: ""}},
		{name: "edge whitespace", request: CreateChannelRequest{ChannelName: " work "}},
		{name: "control character", request: CreateChannelRequest{ChannelName: "wo\x01rk"}},
		{name: "too long", request: CreateChannelRequest{ChannelName: strings.Repeat("a", maxChannelNameBytes+1)}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := board.CreateChannel(context.Background(), root, testCase.request); err == nil {
				t.Fatalf("CreateChannel(%q) succeeded", testCase.request.ChannelName)
			} else if !IsInvalidRequest(err) {
				t.Fatalf("error = %v, want an invalid-request error", err)
			}
		})
	}
	if _, err := board.CreateChannel(context.Background(), root, CreateChannelRequest{ChannelName: "work"}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if _, err := board.CreateChannel(context.Background(), root, CreateChannelRequest{ChannelName: "work"}); err == nil {
		t.Fatal("a duplicate channel was accepted")
	}
	for _, testCase := range []struct {
		name    string
		request PostRequest
	}{
		{
			name:    "empty text",
			request: PostRequest{RequestID: "a", Destination: PostDestination{Kind: "channel", Name: "work"}},
		},
		{
			name: "oversized text",
			request: PostRequest{
				RequestID: "a", Destination: PostDestination{Kind: "channel", Name: "work"},
				Text: strings.Repeat("x", maxPostTextBytes+1),
			},
		},
		{
			name: "missing request id",
			request: PostRequest{
				Destination: PostDestination{Kind: "channel", Name: "work"}, Text: "hello",
			},
		},
		{
			name: "unknown channel",
			request: PostRequest{
				RequestID: "a", Destination: PostDestination{Kind: "channel", Name: "missing"}, Text: "hello",
			},
		},
		{
			name:    "missing destination",
			request: PostRequest{RequestID: "a", Text: "hello"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := board.Post(context.Background(), root, testCase.request); err == nil {
				t.Fatal("invalid post was accepted")
			}
		})
	}
	posted, err := board.Post(context.Background(), root, PostRequest{
		RequestID: "a", Destination: PostDestination{Kind: "channel", Name: "work"}, Text: "hello",
	})
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if _, err := board.ReadThread(context.Background(), root, ReadThreadRequest{ThreadID: "missing"}); err == nil {
		t.Fatal("an unknown thread id was accepted")
	}
	// read_post clamps the offset to the post length.
	content, err := board.ReadPost(context.Background(), root, ReadPostRequest{
		MessageID: posted.MessageID, OffsetChars: 100, LimitChars: 5,
	})
	if err != nil {
		t.Fatalf("ReadPost() error = %v", err)
	}
	if content.Text != "" || content.NextOffsetChars != 5 {
		t.Fatalf("clamped read = %#v", content)
	}
}

func stringPointer(value string) *string { return &value }
