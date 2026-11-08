package agentboard

import (
	"context"
	"strings"
	"testing"
)

// boardPostSequence reads the database-wide insertion position of one post, the
// cursor a fixed read view exposes through AppendedRequestIDs.
func boardPostSequence(t *testing.T, board *LocalAgentMessageBoard, id string) int64 {
	t.Helper()
	var seq int64
	if err := board.db.QueryRowContext(context.Background(), "SELECT seq FROM posts WHERE board=? AND id=?", board.identity, id).Scan(&seq); err != nil {
		t.Fatalf("read sequence of %s: %v", id, err)
	}
	return seq
}

// channelNames renders a channel page for failure messages.
func channelNames(channels []ChannelSummary) []string {
	names := make([]string, 0, len(channels))
	for _, channel := range channels {
		names = append(names, channel.ChannelName)
	}
	return names
}

// channelMessageCount reports the frozen message count of one channel, or -1.
func channelMessageCount(channels []ChannelSummary, name string) int {
	for _, channel := range channels {
		if channel.ChannelName == name {
			return channel.MessageCount
		}
	}
	return -1
}

// TestLocalBoardReadViewServesListSearchAndReadThreadLikeRust pins the fixed
// read view's read surface: identity, storage namespace, the frozen channel
// catalog, list/search/read-thread, whole post bodies by position, and the
// insertion-position pager (Rust #59993 read_view.rs).
func TestLocalBoardReadViewServesListSearchAndReadThreadLikeRust(t *testing.T) {
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
	first, err := board.Post(ctx, root, PostRequest{RequestID: "v-1", Destination: PostDestination{Kind: "channel", Name: "alpha"}, Text: "root text"})
	if err != nil {
		t.Fatalf("Post(root) error = %v", err)
	}
	reply, err := board.Post(ctx, root, PostRequest{RequestID: "v-2", Destination: PostDestination{Kind: "thread", ThreadID: first.MessageID}, Text: "reply text"})
	if err != nil {
		t.Fatalf("Post(reply) error = %v", err)
	}
	if _, err := board.Post(ctx, root, PostRequest{RequestID: "v-3", Destination: PostDestination{Kind: "channel", Name: "beta"}, Text: "beta text"}); err != nil {
		t.Fatalf("Post(beta) error = %v", err)
	}

	view, err := board.CaptureReadView(ctx)
	if err != nil {
		t.Fatalf("CaptureReadView() error = %v", err)
	}

	if got, want := view.Identity(), board.Identity(); got != want || got == "" {
		t.Fatalf("view Identity() = %q, want %q", got, want)
	}
	if got, want := view.StorageNamespace(), DefaultBoardStorageNamespace; got != want {
		t.Fatalf("view StorageNamespace() = %q, want %q", got, want)
	}
	if got, want := view.Channels(), []string{"alpha", "beta"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("view Channels() = %v, want the frozen roster %v", got, want)
	}
	if catalog := view.ChannelCatalog(); len(catalog) != 2 || catalog[0].Permissions == nil || !catalog[0].Permissions.Read || catalog[0].Permissions.Post {
		t.Fatalf("view ChannelCatalog() = %#v, want two read-only channel summaries", catalog)
	}

	// Whole bodies, in the requested order, bounded by the capture.
	rootSeq := boardPostSequence(t, board, first.MessageID)
	replySeq := boardPostSequence(t, board, reply.MessageID)
	texts, err := view.ReadPostTexts(ctx, root, []int64{replySeq, rootSeq})
	if err != nil {
		t.Fatalf("ReadPostTexts() error = %v", err)
	}
	if len(texts) != 2 || texts[0] != "reply text" || texts[1] != "root text" {
		t.Fatalf("ReadPostTexts() = %v, want the requested order [reply text, root text]", texts)
	}
	if _, err := view.ReadPostTexts(ctx, root, []int64{view.Cutoff() + 1}); err == nil || !strings.Contains(err.Error(), "post not found in this board view") {
		t.Fatalf("ReadPostTexts(beyond cutoff) error = %v, want a not-found view error", err)
	}
	many := make([]int64, 0, DefaultPageLimit+1)
	for index := 0; index <= DefaultPageLimit; index++ {
		many = append(many, rootSeq)
	}
	if _, err := view.ReadPostTexts(ctx, root, many); err == nil || !strings.Contains(err.Error(), "requested posts exceed one board page") {
		t.Fatalf("ReadPostTexts(%d positions) error = %v, want the one-page bound", len(many), err)
	}

	// Channel discovery pages the frozen catalog: newest activity first, case
	// folded search, oldest-first reversal and a cursor.
	channels, err := view.ListChannels(ctx, root, ChannelQuery{})
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	// Rust's frozen catalog is ordered by most recent post activity, so beta (the
	// last post) precedes alpha even though alpha sorts first by name.
	if len(channels.Results) != 2 || channels.Results[0].ChannelName != "beta" || channels.Results[1].ChannelName != "alpha" {
		t.Fatalf("ListChannels() = %v, want the newest-activity order [beta alpha]", channelNames(channels.Results))
	}
	if channels.BoardPermissions == nil || channels.BoardPermissions.CreateChannel || channels.BoardPermissions.EditMetadata {
		t.Fatalf("ListChannels() permissions = %#v, want a read-only capture", channels.BoardPermissions)
	}
	if count := channelMessageCount(channels.Results, "alpha"); count != 2 {
		t.Fatalf("frozen alpha message count = %d, want 2", count)
	}
	if count := channelMessageCount(channels.Results, "beta"); count != 1 {
		t.Fatalf("frozen beta message count = %d, want 1", count)
	}
	filtered, err := view.ListChannels(ctx, root, ChannelQuery{Query: pointerTo("ALP")})
	if err != nil {
		t.Fatalf("ListChannels(search) error = %v", err)
	}
	if len(filtered.Results) != 1 || filtered.Results[0].ChannelName != "alpha" {
		t.Fatalf("ListChannels(search) = %v, want only alpha", filtered.Results)
	}
	reversed, err := view.ListChannels(ctx, root, ChannelQuery{Direction: OldestFirst})
	if err != nil {
		t.Fatalf("ListChannels(oldest first) error = %v", err)
	}
	if len(reversed.Results) != 2 || reversed.Results[0].ChannelName != "alpha" {
		t.Fatalf("ListChannels(oldest first) = %v, want the reversed catalog [alpha beta]", channelNames(reversed.Results))
	}
	firstPage, err := view.ListChannels(ctx, root, ChannelQuery{Page: PageRequest{Limit: 1}})
	if err != nil {
		t.Fatalf("ListChannels(limit 1) error = %v", err)
	}
	if len(firstPage.Results) != 1 || firstPage.Results[0].ChannelName != "beta" || firstPage.NextCursor == nil {
		t.Fatalf("ListChannels(limit 1) = %v (cursor %v), want beta with a cursor", channelNames(firstPage.Results), firstPage.NextCursor)
	}
	secondPage, err := view.ListChannels(ctx, root, ChannelQuery{Page: PageRequest{Limit: 1, Cursor: firstPage.NextCursor}})
	if err != nil {
		t.Fatalf("ListChannels(cursor) error = %v", err)
	}
	if len(secondPage.Results) != 1 || secondPage.Results[0].ChannelName != "alpha" || secondPage.NextCursor != nil {
		t.Fatalf("ListChannels(cursor) = %v, want the exhausted last page [alpha]", channelNames(secondPage.Results))
	}

	// Threads, search and one thread read.
	threads, err := view.ListThreads(ctx, root, ThreadQuery{ChannelName: "alpha"})
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if len(threads.Results) != 1 || threads.Results[0].ThreadID != first.MessageID || threads.Results[0].ReplyCount != 1 {
		t.Fatalf("ListThreads() = %v, want the one alpha thread with one reply", threads.Results)
	}
	if threads.Results[0].LatestReply == nil || threads.Results[0].LatestReply.TextPreview != "reply text" {
		t.Fatalf("ListThreads() latest reply = %#v, want the captured reply", threads.Results[0].LatestReply)
	}
	posts, err := view.SearchPosts(ctx, root, PostQuery{ChannelName: pointerTo("alpha")})
	if err != nil {
		t.Fatalf("SearchPosts() error = %v", err)
	}
	if len(posts.Results) != 2 {
		t.Fatalf("SearchPosts(alpha) returned %d posts, want the captured root and reply", len(posts.Results))
	}
	thread, err := view.ReadThread(ctx, root, ReadThreadRequest{ThreadID: first.MessageID})
	if err != nil {
		t.Fatalf("ReadThread() error = %v", err)
	}
	if thread.RootPost.TextPreview != "root text" || len(thread.Replies.Results) != 1 || thread.Replies.Results[0].TextPreview != "reply text" {
		t.Fatalf("ReadThread() = %#v / %#v, want the captured root and reply", thread.RootPost, thread.Replies.Results)
	}

	// Insertion positions page in ascending sequence order and strip the caller
	// prefix from the persisted retry key.
	positions, err := view.AppendedRequestIDs(ctx, root, "alpha", 0)
	if err != nil {
		t.Fatalf("AppendedRequestIDs() error = %v", err)
	}
	if len(positions) != 2 || positions[0].RequestID != "v-1" || positions[1].RequestID != "v-2" {
		t.Fatalf("AppendedRequestIDs() = %v, want v-1 then v-2", positions)
	}
	if positions[0].Sequence != rootSeq || positions[1].Sequence != replySeq {
		t.Fatalf("AppendedRequestIDs() sequences = %d,%d, want %d,%d", positions[0].Sequence, positions[1].Sequence, rootSeq, replySeq)
	}
	tail, err := view.AppendedRequestIDs(ctx, root, "alpha", replySeq)
	if err != nil {
		t.Fatalf("AppendedRequestIDs(after last) error = %v", err)
	}
	if len(tail) != 0 {
		t.Fatalf("AppendedRequestIDs(after last) = %v, want the page exhausted", tail)
	}
	if _, err := view.AppendedRequestIDs(ctx, root, "alpha", -1); err == nil || !strings.Contains(err.Error(), "post cursor is outside the captured board view") {
		t.Fatalf("AppendedRequestIDs(-1) error = %v, want the outside-the-view rejection", err)
	}
	if _, err := view.AppendedRequestIDs(ctx, root, "alpha", view.Cutoff()+1); err == nil || !strings.Contains(err.Error(), "post cursor is outside the captured board view") {
		t.Fatalf("AppendedRequestIDs(beyond cutoff) error = %v, want the outside-the-view rejection", err)
	}
}

// TestLocalBoardReadViewSearchHonorsCutoffAfterBackdatingLikeRust pins that a
// post appended after the capture stays invisible to every read on the view -
// including a backdated one whose timestamp precedes the capture - while the
// live board keeps serving it.
func TestLocalBoardReadViewSearchHonorsCutoffAfterBackdatingLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	ctx := context.Background()
	board := openLocalBoard(t, sqlite, root, host)
	defer board.Close()

	if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "alpha", Subscription: Unsubscribe}); err != nil {
		t.Fatalf("CreateChannel(alpha) error = %v", err)
	}
	early, err := board.Post(ctx, root, PostRequest{RequestID: "cut-1", Destination: PostDestination{Kind: "channel", Name: "alpha"}, Text: "before capture"})
	if err != nil {
		t.Fatalf("Post(before) error = %v", err)
	}
	view, err := board.CaptureReadView(ctx)
	if err != nil {
		t.Fatalf("CaptureReadView() error = %v", err)
	}

	late, err := board.Post(ctx, root, PostRequest{RequestID: "cut-2", Destination: PostDestination{Kind: "channel", Name: "alpha"}, Text: "after capture"})
	if err != nil {
		t.Fatalf("Post(after) error = %v", err)
	}
	// Backdating precedes the capture in time but not in sequence.
	if _, err := board.db.ExecContext(ctx, `UPDATE posts SET timestamp = 1 WHERE id = ?`, late.MessageID); err != nil {
		t.Fatalf("backdate post: %v", err)
	}
	if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "omega", Subscription: Unsubscribe}); err != nil {
		t.Fatalf("CreateChannel(omega) error = %v", err)
	}

	// The live board serves both posts and the new channel.
	live, err := board.SearchPosts(ctx, root, PostQuery{ChannelName: pointerTo("alpha")})
	if err != nil {
		t.Fatalf("live SearchPosts() error = %v", err)
	}
	if len(live.Results) != 2 {
		t.Fatalf("live SearchPosts(alpha) returned %d posts, want the captured and the backdated one", len(live.Results))
	}
	liveChannels, err := board.ListChannels(ctx, root, ChannelQuery{})
	if err != nil {
		t.Fatalf("live ListChannels() error = %v", err)
	}
	if len(liveChannels.Results) != 2 {
		t.Fatalf("live ListChannels() = %d channels, want the post-capture channel too", len(liveChannels.Results))
	}

	// The view hides the backdated post from every read.
	searched, err := view.SearchPosts(ctx, root, PostQuery{ChannelName: pointerTo("alpha")})
	if err != nil {
		t.Fatalf("view SearchPosts() error = %v", err)
	}
	if len(searched.Results) != 1 || searched.Results[0].MessageID != early.MessageID {
		t.Fatalf("view SearchPosts(alpha) = %v, want only the captured post", searched.Results)
	}
	if _, err := view.SearchPosts(ctx, root, PostQuery{Query: pointerTo("after capture")}); err != nil {
		t.Fatalf("view SearchPosts(text) error = %v", err)
	} else if looked, err := view.SearchPosts(ctx, root, PostQuery{Query: pointerTo("after capture")}); err == nil && len(looked.Results) != 0 {
		t.Fatalf("view SearchPosts found the backdated post: %v", looked.Results)
	}
	threads, err := view.ListThreads(ctx, root, ThreadQuery{ChannelName: "alpha"})
	if err != nil {
		t.Fatalf("view ListThreads() error = %v", err)
	}
	if len(threads.Results) != 1 || threads.Results[0].ThreadID != early.MessageID {
		t.Fatalf("view ListThreads(alpha) = %v, want only the captured thread", threads.Results)
	}
	if _, err := view.ReadThread(ctx, root, ReadThreadRequest{ThreadID: late.MessageID}); err == nil || !strings.Contains(err.Error(), "post not found in this board") {
		t.Fatalf("view ReadThread(backdated) error = %v, want not found", err)
	}
	if _, err := view.ReadPost(ctx, root, ReadPostRequest{MessageID: late.MessageID}); err == nil || !strings.Contains(err.Error(), "post not found in this board") {
		t.Fatalf("view ReadPost(backdated) error = %v, want not found", err)
	}
	if _, err := view.ReadPostTexts(ctx, root, []int64{boardPostSequence(t, board, late.MessageID)}); err == nil {
		t.Fatal("view ReadPostTexts served a backdated post appended after the capture")
	}
	positions, err := view.AppendedRequestIDs(ctx, root, "alpha", 0)
	if err != nil {
		t.Fatalf("view AppendedRequestIDs() error = %v", err)
	}
	if len(positions) != 1 || positions[0].RequestID != "cut-1" {
		t.Fatalf("view AppendedRequestIDs() = %v, want only the captured position", positions)
	}

	// The catalog is frozen with its capture-time counts.
	channels, err := view.ListChannels(ctx, root, ChannelQuery{})
	if err != nil {
		t.Fatalf("view ListChannels() error = %v", err)
	}
	if len(channels.Results) != 1 || channels.Results[0].ChannelName != "alpha" {
		t.Fatalf("view ListChannels() = %v, want only the captured channel", channels.Results)
	}
	if channels.Results[0].MessageCount != 1 {
		t.Fatalf("frozen alpha message count = %d, want the capture-time count 1", channels.Results[0].MessageCount)
	}
	// A channel outside the frozen roster is rejected rather than served live.
	if _, err := view.ListThreads(ctx, root, ThreadQuery{ChannelName: "omega"}); err == nil || !strings.Contains(err.Error(), "channel not found in this board") {
		t.Fatalf("view ListThreads(post-capture channel) error = %v, want channel not found", err)
	}
}
