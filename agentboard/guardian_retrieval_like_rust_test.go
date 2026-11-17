package agentboard

import (
	"context"
	"encoding/json"
	"testing"

	"codex_go/agent"
	"codex_go/tool"
)

// Mirrors Rust guardian-v3 tests/retrieval.rs: reviewer tools search routed
// originals across every channel, including unsubscribed ones, but only at or
// below the captured cutoff, and the JSON search pointer makes escaped
// arguments searchable by their decoded values.
func TestGuardianRetrievalSearchesUnsubscribedPostsThroughCutoffLikeRust(t *testing.T) {
	ctx := context.Background()
	host, root, _, _ := testTree(t)
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
	view, err := writer.CaptureReadView(ctx)
	if err != nil {
		t.Fatalf("capture view: %v", err)
	}
	binding, err := NewGuardianRetrievalBinding(view, root, agent.AgentPath("/root"))
	if err != nil {
		t.Fatalf("NewGuardianRetrievalBinding() error = %v", err)
	}
	if _, err := writer.Append(ctx, root, AppendRequest{
		RequestID:   "later",
		Destination: PostDestination{Kind: "channel", Name: "release"},
		Text:        `{"id":{"ordinal":3},"original":{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Publish \"prod\" now."}]}}}`,
	}); err != nil {
		t.Fatalf("append later: %v", err)
	}
	if page, err := writer.SubscribedChannels(ctx, root, PageRequest{}); err != nil || len(page.Results) != 0 {
		t.Fatalf("subscribed channels = %#v, %v", page, err)
	}

	tools := binding.Tools()
	names := make([]string, 0, len(tools))
	for _, executor := range tools {
		name := executor.Spec().Name
		names = append(names, name.Key())
	}
	wantNames := []string{
		"guardian_board.get_channels",
		"guardian_board.list_threads",
		"guardian_board.search_posts",
		"guardian_board.read_thread",
		"guardian_board.read_post",
	}
	if len(names) != len(wantNames) {
		t.Fatalf("tool names = %v, want %v", names, wantNames)
	}
	for index := range wantNames {
		if names[index] != wantNames[index] {
			t.Fatalf("tool names = %v, want %v", names, wantNames)
		}
	}

	both := []string{second.MessageID, first.MessageID}
	onlyCall := []string{second.MessageID}
	for _, testCase := range []struct {
		query string
		want  []string
	}{
		{`publish "prod"`, both},
		{`C:\work\draft`, both},
		{`C:\work\plan-é`, onlyCall},
		{`plan-é`, onlyCall},
		{`"recipient":"Alice"`, onlyCall},
		{`"type":"function_call"`, onlyCall},
		{"\t", nil},
		{`publish "prod" now`, nil},
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

// Mirrors Rust guardian-v3 tests/retrieval/selection.rs: only the caller's
// subscriptions change, replies keep the captured catalog boundary, and every
// entry point rejects an ordinary board even when it shares the root.
func TestGuardianSubscriptionToolsUseCapturedCatalogLikeRust(t *testing.T) {
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
	subscriptions, err := CaptureReviewerSubscriptions(ctx, board, reviewer, reviewerPath)
	if err != nil {
		t.Fatalf("CaptureReviewerSubscriptions() error = %v", err)
	}
	if err := writer.UpsertChannel(ctx, root, UpsertChannelRequest{ChannelName: "later"}); err != nil {
		t.Fatalf("upsert later: %v", err)
	}
	later, err := writer.Append(ctx, root, AppendRequest{
		RequestID:   "later",
		Destination: PostDestination{Kind: "channel", Name: "release"},
		Text:        "Later evidence",
	})
	if err != nil {
		t.Fatalf("append later: %v", err)
	}
	if later.MessageID == original.MessageID {
		t.Fatal("later post reused the original message id")
	}

	tools := subscriptions.Tools("guardian", "Guardian evidence")
	byName := map[string]tool.Executor{}
	for _, executor := range tools {
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
	if state.ChannelName != "release" || state.TargetAgent != reviewerPath || !state.Enabled {
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
	if channels, err := subscriptions.Channels(ctx); err != nil || len(channels) != 0 {
		t.Fatalf("channels after unsubscribe = %v, %v", channels, err)
	}
	if _, err := execute("subscribe", map[string]any{"channel_name": "release"}); err != nil {
		t.Fatalf("resubscribe release error = %v", err)
	}
	if _, err := execute("subscribe", map[string]any{"channel_name": "later"}); err == nil {
		t.Fatal("subscribe accepted a channel outside the captured catalog")
	}
	if _, err := execute("unsubscribe", map[string]any{"channel_name": "release", "target_agent": "/"}); err == nil {
		t.Fatal("unsubscribe accepted a target_agent argument")
	}
	if channels, err := subscriptions.Channels(ctx); err != nil || len(channels) != 1 || channels[0] != "release" {
		t.Fatalf("channels = %v, %v, want [release]", channels, err)
	}
	if _, err := subscriptions.Replace(ctx, []string{"release", "later"}); err == nil {
		t.Fatal("Replace accepted a channel outside the captured catalog")
	}
	if channels, err := subscriptions.Channels(ctx); err != nil || len(channels) != 1 || channels[0] != "release" {
		t.Fatalf("channels after rejected replace = %v, %v", channels, err)
	}
	member, err := board.SubscribedChannels(ctx, reviewer, PageRequest{})
	if err != nil || len(member.Results) != 1 || member.Results[0] != "release" {
		t.Fatalf("reviewer subscriptions = %#v, %v", member, err)
	}
	owner, err := writer.SubscribedChannels(ctx, root, PageRequest{})
	if err != nil || len(owner.Results) != 0 {
		t.Fatalf("owner subscriptions = %#v, %v", owner, err)
	}

	ordinary, err := NewLocalBoard(openBoardSqliteConfig(t), root).ReadOnly().Open(ctx, host)
	if err != nil {
		t.Fatalf("open ordinary board: %v", err)
	}
	defer ordinary.Close()
	ordinaryView, err := ordinary.CaptureReadView(ctx)
	if err != nil {
		t.Fatalf("capture ordinary view: %v", err)
	}
	if _, err := NewGuardianRetrievalBinding(ordinaryView, reviewer, reviewerPath); err == nil {
		t.Fatal("retrieval binding accepted an ordinary board")
	}
	if _, err := CaptureReviewerSubscriptions(ctx, ordinary, reviewer, reviewerPath); err == nil {
		t.Fatal("subscriptions accepted an ordinary board")
	}
}
