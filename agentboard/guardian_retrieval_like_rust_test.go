package agentboard

import (
	"context"
	"encoding/json"
	"testing"

	"codex_go/tool"
)

// Mirrors Rust guardian-v3 tests/retrieval.rs after #62133: reviewer tools read
// the live board, so evidence routed after the review started stays visible,
// searches reach channels the reviewer does not follow, and the JSON pointer
// makes escaped arguments searchable by their decoded values.
func TestGuardianRetrievalSearchesLiveUnsubscribedPostsLikeRust(t *testing.T) {
	ctx := context.Background()
	host, root, reviewer, reviewerPath := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	writer, err := NewGuardianLocalBoard(sqlite, root).ReadWrite().Open(ctx, host)
	if err != nil {
		t.Fatalf("open guardian board: %v", err)
	}
	defer writer.Close()

	earlier := `{"id":{"ordinal":1},"original":{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Do not publish \"prod\" from C:\\work\\draft."}]}}}`
	first, err := writer.Append(ctx, root, AppendRequest{
		RequestID:   "first",
		Destination: PostDestination{Kind: "new_channel", Name: "release"},
		Text:        earlier,
	})
	if err != nil {
		t.Fatalf("append first: %v", err)
	}
	call := `{"id":{"ordinal":2},"original":{"type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call-2","arguments":"{\"recipient\":\"Alice\",\"cmd\":\"publish \\\"prod\\\" from C:\\\\work\\\\draft\",\"paths\":[\"C:\\\\tmp\",\"C:\\\\work\\\\plan-\\u00c9\"]}"}}}`
	second, err := writer.Append(ctx, root, AppendRequest{
		RequestID:   "second",
		Destination: PostDestination{Kind: "channel", Name: "release"},
		Text:        call,
	})
	if err != nil {
		t.Fatalf("append second: %v", err)
	}

	board, err := NewGuardianLocalBoard(sqlite, root).ReadOnly().Open(ctx, host)
	if err != nil {
		t.Fatalf("open reviewer board: %v", err)
	}
	defer board.Close()
	binding, err := NewGuardianRetrievalBinding(board, reviewer, reviewerPath)
	if err != nil {
		t.Fatalf("NewGuardianRetrievalBinding() error = %v", err)
	}

	// Evidence routed after the reviewer started stays visible: the live board
	// has no capture cutoff.
	later, err := writer.Append(ctx, root, AppendRequest{
		RequestID:   "later",
		Destination: PostDestination{Kind: "channel", Name: "release"},
		Text:        `{"id":{"ordinal":3},"original":{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Publish \"prod\" now."}]}}}`,
	})
	if err != nil {
		t.Fatalf("append later: %v", err)
	}
	if page, err := board.SubscribedChannels(ctx, reviewer, PageRequest{}); err != nil || len(page.Results) != 0 {
		t.Fatalf("reviewer subscriptions = %#v, %v", page, err)
	}

	tools := binding.Tools()
	wantNames := []string{
		"guardian_board.get_channels",
		"guardian_board.list_threads",
		"guardian_board.search_posts",
		"guardian_board.read_thread",
		"guardian_board.read_post",
		"guardian_board.subscribed_channels",
		"guardian_board.subscribe",
		"guardian_board.unsubscribe",
	}
	if names := guardianToolNames(tools); len(names) != len(wantNames) {
		t.Fatalf("tool names = %v, want %v", names, wantNames)
	} else {
		for index := range wantNames {
			if names[index] != wantNames[index] {
				t.Fatalf("tool names = %v, want %v", names, wantNames)
			}
		}
	}
	if names := binding.ToolNames(); len(names) != len(wantNames) {
		t.Fatalf("ToolNames() = %v, want %v", names, wantNames)
	}

	both := []string{later.MessageID, second.MessageID, first.MessageID}
	onlyCall := []string{second.MessageID}
	for _, testCase := range []struct {
		query string
		want  []string
	}{
		{`publish "prod"`, both},
		{`C:\work\draft`, []string{second.MessageID, first.MessageID}},
		{`C:\work\plan-é`, onlyCall},
		{`plan-é`, onlyCall},
		{`"recipient":"Alice"`, onlyCall},
		{`"type":"function_call"`, onlyCall},
		{"\t", nil},
	} {
		got := guardianSearchIDs(t, tools, testCase.query)
		if len(got) != len(testCase.want) {
			t.Fatalf("search %q = %v, want %v", testCase.query, got, testCase.want)
		}
		for index := range testCase.want {
			if got[index] != testCase.want[index] {
				t.Fatalf("search %q = %v, want %v", testCase.query, got, testCase.want)
			}
		}
	}

	ordinary, err := NewLocalBoard(openBoardSqliteConfig(t), root).ReadOnly().Open(ctx, host)
	if err != nil {
		t.Fatalf("open ordinary board: %v", err)
	}
	defer ordinary.Close()
	if _, err := NewGuardianRetrievalBinding(ordinary, reviewer, reviewerPath); err == nil {
		t.Fatal("retrieval binding accepted an ordinary board")
	}
}

// Mirrors Rust guardian-v3 tests/retrieval/subscriptions.rs: the reviewer changes
// only its own subscriptions, channels created after the binding are reachable,
// and the listed subscriptions page like every other board read.
func TestGuardianSubscriptionToolsChangeOnlyCallerLikeRust(t *testing.T) {
	ctx := context.Background()
	host, root, reviewer, reviewerPath := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	writer, err := NewGuardianLocalBoard(sqlite, root).ReadWrite().Open(ctx, host)
	if err != nil {
		t.Fatalf("open guardian board: %v", err)
	}
	defer writer.Close()
	if err := writer.UpsertChannel(ctx, root, UpsertChannelRequest{ChannelName: "release"}); err != nil {
		t.Fatalf("upsert release: %v", err)
	}
	original, err := writer.Append(ctx, root, AppendRequest{
		RequestID:   "original",
		Destination: PostDestination{Kind: "channel", Name: "release"},
		Text:        "Original",
	})
	if err != nil {
		t.Fatalf("append original: %v", err)
	}

	board, err := NewGuardianLocalBoard(sqlite, root).ReadOnly().Open(ctx, host)
	if err != nil {
		t.Fatalf("open reviewer board: %v", err)
	}
	defer board.Close()
	binding, err := NewGuardianRetrievalBinding(board, reviewer, reviewerPath)
	if err != nil {
		t.Fatalf("NewGuardianRetrievalBinding() error = %v", err)
	}
	// A channel created after the binding is a live channel, not a catalog miss.
	if err := writer.UpsertChannel(ctx, root, UpsertChannelRequest{ChannelName: "later"}); err != nil {
		t.Fatalf("upsert later: %v", err)
	}

	byName := map[string]tool.Executor{}
	for _, executor := range binding.Tools() {
		byName[executor.Spec().Name.Name] = executor
	}
	execute := func(name string, arguments map[string]any) (*tool.Output, error) {
		return byName[name].Execute(ctx, boardInvocation(t, name, arguments))
	}

	output, err := execute("subscribe", map[string]any{"channel_name": "release"})
	if err != nil {
		t.Fatalf("subscribe release error = %v", err)
	}
	var state SubscriptionState
	if err := json.Unmarshal([]byte(output.Body), &state); err != nil {
		t.Fatalf("decode subscribe state: %v", err)
	}
	if state.ChannelName != "release" || state.TargetAgent != reviewerPath || !state.Enabled || state.ThreadID != nil {
		t.Fatalf("subscribe state = %#v", state)
	}
	if state.LastMessageID == nil || *state.LastMessageID != original.MessageID {
		t.Fatalf("subscribe last_message_id = %v, want %q", state.LastMessageID, original.MessageID)
	}

	if output, err = execute("unsubscribe", map[string]any{"channel_name": "release"}); err != nil {
		t.Fatalf("unsubscribe release error = %v", err)
	}
	if err := json.Unmarshal([]byte(output.Body), &state); err != nil {
		t.Fatalf("decode unsubscribe state: %v", err)
	}
	if state.Enabled {
		t.Fatalf("unsubscribe state = %#v", state)
	}
	if channels, err := board.SubscribedChannels(ctx, reviewer, PageRequest{}); err != nil || len(channels.Results) != 0 {
		t.Fatalf("channels after unsubscribe = %v, %v", channels, err)
	}

	if _, err := execute("subscribe", map[string]any{"channel_name": "later"}); err != nil {
		t.Fatalf("subscribe later error = %v", err)
	}
	if _, err := execute("subscribe", map[string]any{"channel_name": "release"}); err != nil {
		t.Fatalf("resubscribe release error = %v", err)
	}
	if _, err := execute("unsubscribe", map[string]any{"channel_name": "release", "target_agent": "/"}); err == nil {
		t.Fatal("unsubscribe accepted a target_agent argument")
	}

	first, err := execute("subscribed_channels", map[string]any{"limit": 1})
	if err != nil {
		t.Fatalf("subscribed_channels(first) error = %v", err)
	}
	var page struct {
		Results    []string `json:"results"`
		NextCursor *string  `json:"next_cursor"`
	}
	if err := json.Unmarshal([]byte(first.Body), &page); err != nil {
		t.Fatalf("decode subscribed_channels: %v", err)
	}
	if len(page.Results) != 1 || page.NextCursor == nil {
		t.Fatalf("first page = %#v, %v", page.Results, page.NextCursor)
	}
	second, err := execute("subscribed_channels", map[string]any{"limit": 1, "cursor": *page.NextCursor})
	if err != nil {
		t.Fatalf("subscribed_channels(second) error = %v", err)
	}
	if err := json.Unmarshal([]byte(second.Body), &page); err != nil {
		t.Fatalf("decode second subscribed_channels: %v", err)
	}
	if len(page.Results) != 1 || page.NextCursor != nil {
		t.Fatalf("second page = %#v, %v", page.Results, page.NextCursor)
	}

	// Only the caller changed: the owning root still follows nothing.
	if owner, err := writer.SubscribedChannels(ctx, root, PageRequest{}); err != nil || len(owner.Results) != 0 {
		t.Fatalf("owner subscriptions = %#v, %v", owner, err)
	}
}

func guardianToolNames(tools []tool.Executor) []string {
	names := make([]string, 0, len(tools))
	for _, executor := range tools {
		name := executor.Spec().Name
		names = append(names, name.Key())
	}
	return names
}

func guardianSearchIDs(t *testing.T, tools []tool.Executor, query string) []string {
	t.Helper()
	var search tool.Executor
	for _, executor := range tools {
		if executor.Spec().Name.Name == string(MessageBoardToolSearchPosts) {
			search = executor
			break
		}
	}
	if search == nil {
		t.Fatal("missing search_posts tool")
	}
	output, err := search.Execute(context.Background(), boardInvocation(t, "search_posts", map[string]any{"query": query}))
	if err != nil {
		t.Fatalf("search %q error = %v", query, err)
	}
	var page struct {
		Results []PostPreview `json:"results"`
	}
	if err := json.Unmarshal([]byte(output.Body), &page); err != nil {
		t.Fatalf("decode search %q body %q: %v", query, output.Body, err)
	}
	ids := make([]string, 0, len(page.Results))
	for _, result := range page.Results {
		ids = append(ids, result.MessageID)
	}
	return ids
}
