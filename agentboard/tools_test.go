package agentboard

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"codex_go/agent"
	"codex_go/tool"
	"codex_go/utils"
)

// Rust parity: codex-rs/ext/agent-message-board/src/tools/spec.rs. The expected
// schemas are written out literally so the test does not share the production
// field-schema table.
func TestMessageBoardToolSpecsMatchRust(t *testing.T) {
	// Rust's fallback arm is a bare string schema with no description.
	plainString := map[string]any{"type": "string"}
	agentPathField := map[string]any{"type": "string", "description": "An absolute agent path or a reference relative to you."}
	limitField := map[string]any{"type": "integer", "minimum": 1, "description": "Maximum results, default 20; output budgets may return fewer. Continue with next_cursor."}
	cursorField := map[string]any{"type": "string", "description": "Opaque next_cursor from the same query. Keep filters and sorting unchanged. Concurrent posts may shift pages; omit the cursor to refresh."}
	charsField := map[string]any{"type": "integer", "minimum": 1, "description": "Maximum characters; further capped by output budget. Defaults: limit_chars 20000, max_chars_per_post 1000."}

	tests := []struct {
		name        string
		description string
		properties  map[string]any
		required    []string
		parallel    bool
	}{
		{
			name:        "create_channel",
			description: "Create a channel where all agents in this collaboration can read and post messages. You are subscribed to new top-level posts by default.",
			properties: map[string]any{
				"channel_name": plainString,
				"subscribe":    map[string]any{"type": "boolean", "description": "Subscribe to new top-level posts. Default true."},
			},
			required: []string{"channel_name"},
		},
		{
			name:        "get_channels",
			description: "List channels, most recently active first, or search by a case-insensitive part of the name. Creating a channel or posting in it counts as activity.",
			properties: map[string]any{
				"query":        plainString,
				"recent_first": map[string]any{"type": "boolean", "description": "Newest first by default."},
				"limit":        limitField,
				"cursor":       cursorField,
			},
			required: []string{},
			parallel: true,
		},
		{
			name:        "list_threads",
			description: "List a channel's threads with previews of the first post and latest reply. New threads come first by default; sorting by activity brings threads with recent replies to the top.",
			properties: map[string]any{
				"channel_name":       plainString,
				"sort":               map[string]any{"type": "string", "enum": []string{"created", "activity"}},
				"recent_first":       map[string]any{"type": "boolean", "description": "Newest first by default."},
				"limit":              limitField,
				"cursor":             cursorField,
				"max_chars_per_post": charsField,
			},
			required: []string{"channel_name"},
			parallel: true,
		},
		{
			name:        "search_posts",
			description: "Search top-level posts and replies, newest first. Text matches are case-insensitive substrings; omit the query to see recent activity. You can narrow by channel, author, or posts after a message ID. Results are previews; read_post can retrieve the full text.",
			properties: map[string]any{
				"channel_name":       plainString,
				"query":              plainString,
				"after_message_id":   plainString,
				"author":             agentPathField,
				"limit":              limitField,
				"cursor":             cursorField,
				"max_chars_per_post": charsField,
			},
			required: []string{},
			parallel: true,
		},
		{
			name:        "read_thread",
			description: "Read a thread using its first post's message ID. Every page includes previews of the first post and the newest replies; the cursor advances through replies.",
			properties: map[string]any{
				"thread_id":          plainString,
				"limit":              limitField,
				"cursor":             cursorField,
				"max_chars_per_post": charsField,
			},
			required: []string{"thread_id"},
			parallel: true,
		},
		{
			name:        "read_post",
			description: "Read a post or reply by message ID, without needing its channel. Offsets count Unicode characters; continue at next_offset_chars while it is less than n_chars.",
			properties: map[string]any{
				"message_id":   plainString,
				"offset_chars": map[string]any{"type": "integer", "minimum": 0, "description": "Default 0."},
				"limit_chars":  charsField,
			},
			required: []string{"message_id"},
			parallel: true,
		},
		{
			name:        "subscribe",
			description: "Subscribe yourself or another agent to a channel for new top-level posts, or to a thread for replies. Provide exactly one of channel_name or thread_id. Notifications only reach agents with a running turn; missed notifications are not saved.",
			properties: map[string]any{
				"channel_name": plainString,
				"thread_id":    plainString,
				"target_agent": agentPathField,
			},
			required: []string{},
		},
		{
			name:        "unsubscribe",
			description: "Unsubscribe yourself or another agent from a channel or thread. Provide exactly one of channel_name or thread_id. Posting again does not undo a thread unsubscribe. Agents explicitly named on a post can still receive that notification.",
			properties: map[string]any{
				"channel_name": plainString,
				"thread_id":    plainString,
				"target_agent": agentPathField,
			},
			required: []string{},
		},
		{
			name:        "post",
			description: "Start a thread in an existing or new channel, or reply using the first post's message ID as thread_id. Exactly one destination is required. Posting subscribes you to the thread unless you previously unsubscribed. agents_to_notify sends a one-time notification without subscribing recipients or starting idle agents. Returns metadata, not the post text.",
			properties: map[string]any{
				"text":             plainString,
				"channel_name":     plainString,
				"new_channel_name": map[string]any{"type": "string", "description": "Create and subscribe to this channel."},
				"thread_id":        plainString,
				"agents_to_notify": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Absolute agent paths or references relative to you; maximum 256."},
			},
			required: []string{"text"},
		},
	}

	if len(tests) != len(MessageBoardToolNames) {
		t.Fatalf("spec table covers %d tools, want %d", len(tests), len(MessageBoardToolNames))
	}
	executors := NewMessageBoardTools(&InMemoryBoard{}, "caller", agent.AgentPathRoot, "", "")
	byName := map[string]tool.Spec{}
	for _, executor := range executors {
		byName[executor.Spec().Name.Name] = executor.Spec()
	}
	for _, test := range tests {
		spec, ok := byName[test.name]
		if !ok {
			t.Fatalf("missing tool %s", test.name)
		}
		if spec.Description != test.description {
			t.Fatalf("%s description = %q", test.name, spec.Description)
		}
		expected := map[string]any{
			"type":                 "object",
			"properties":           test.properties,
			"required":             test.required,
			"additionalProperties": false,
		}
		if !reflect.DeepEqual(spec.InputSchema, expected) {
			t.Fatalf("%s schema = %#v, want %#v", test.name, spec.InputSchema, expected)
		}
		if spec.Parallel != test.parallel {
			t.Fatalf("%s parallel = %v, want %v", test.name, spec.Parallel, test.parallel)
		}
		if spec.Name.Namespace != "" || spec.NamespaceDescription != "" {
			t.Fatalf("%s plain spec carries namespace metadata: %#v", test.name, spec)
		}
	}

	// A host-supplied namespace names every tool and decorates the merged
	// namespace; a catalog description replaces only the description.
	override := "Catalog post."
	namespaced := NewMessageBoardToolsWithOverrides(&InMemoryBoard{}, "caller", agent.AgentPathRoot, "delegation", "Shared discussion.", map[string]MessageBoardToolOverride{
		"post": {Description: &override},
	})
	for _, executor := range namespaced {
		spec := executor.Spec()
		if spec.Name.Namespace != "delegation" || spec.NamespaceDescription != "Shared discussion." {
			t.Fatalf("%s namespace = %q/%q", spec.Name.Name, spec.Name.Namespace, spec.NamespaceDescription)
		}
		want := messageBoardSpecFor(spec.Name.Name).description
		if spec.Name.Name == "post" {
			want = override
		}
		if spec.Description != want {
			t.Fatalf("%s description = %q, want %q", spec.Name.Name, spec.Description, want)
		}
	}
}

func TestMessageBoardToolsRunBoardScenariosLikeRust(t *testing.T) {
	host, root, child, _ := testTree(t)
	boards := &InMemoryMessageBoards{}
	board := boards.Open(root, host)
	defer board.Close()
	execs := boardToolSet(t, board, root, agent.AgentPathRoot)

	created, err := boardToolBody(t, execs["create_channel"], boardInvocation(t, "create_channel", map[string]any{"channel_name": "work"}))
	if err != nil {
		t.Fatalf("create_channel error = %v", err)
	}
	if created["channel_name"] != "work" || created["created_by"] != "/root" {
		t.Fatalf("create_channel body = %#v", created)
	}

	// The channel already exists, so the root post targets it by name.
	posted, err := boardToolBody(t, execs["post"], boardInvocation(t, "post", map[string]any{"channel_name": "work", "text": "first"}))
	if err != nil {
		t.Fatalf("post error = %v", err)
	}
	rootID, _ := posted["message_id"].(string)
	if rootID == "" || posted["channel_name"] != "work" || posted["author"] != "/root" {
		t.Fatalf("post body = %#v", posted)
	}

	reply, err := boardToolBody(t, execs["post"], boardInvocation(t, "post", map[string]any{"thread_id": rootID, "text": "second"}))
	if err != nil {
		t.Fatalf("reply error = %v", err)
	}
	if reply["thread_id"] != rootID {
		t.Fatalf("reply thread = %#v, want %s", reply["thread_id"], rootID)
	}

	// A read tool returns the shared Page envelope and the board's previews.
	channels, err := boardToolBody(t, execs["get_channels"], boardInvocation(t, "get_channels", map[string]any{}))
	if err != nil {
		t.Fatalf("get_channels error = %v", err)
	}
	if channels["n_returned"] != float64(1) || channels["has_more"] != false {
		t.Fatalf("get_channels page = %#v", channels)
	}
	summaries, _ := channels["results"].([]any)
	if len(summaries) != 1 {
		t.Fatalf("get_channels results = %#v", channels["results"])
	}
	summary, _ := summaries[0].(map[string]any)
	if summary["channel_name"] != "work" || summary["message_count"] != float64(2) {
		t.Fatalf("channel summary = %#v", summary)
	}

	threadPage, err := boardToolBody(t, execs["read_thread"], boardInvocation(t, "read_thread", map[string]any{"thread_id": rootID}))
	if err != nil {
		t.Fatalf("read_thread error = %v", err)
	}
	rootPost, _ := threadPage["root_post"].(map[string]any)
	if rootPost["message_id"] != rootID || rootPost["text_preview"] != "first" {
		t.Fatalf("root post = %#v", rootPost)
	}
	replies, _ := threadPage["replies"].(map[string]any)
	if replies["n_returned"] != float64(1) {
		t.Fatalf("replies = %#v", replies)
	}

	content, err := boardToolBody(t, execs["read_post"], boardInvocation(t, "read_post", map[string]any{"message_id": rootID, "offset_chars": 1, "limit_chars": 2}))
	if err != nil {
		t.Fatalf("read_post error = %v", err)
	}
	if content["text"] != "ir" || content["n_chars"] != float64(5) || content["next_offset_chars"] != float64(3) {
		t.Fatalf("read_post body = %#v", content)
	}

	found, err := boardToolBody(t, execs["search_posts"], boardInvocation(t, "search_posts", map[string]any{"query": "SECOND", "author": "worker"}))
	if err != nil {
		t.Fatalf("search_posts error = %v", err)
	}
	if found["n_returned"] != float64(0) {
		t.Fatalf("search by child author = %#v, want no matches", found)
	}
	all, err := boardToolBody(t, execs["search_posts"], boardInvocation(t, "search_posts", map[string]any{"query": "second"}))
	if err != nil {
		t.Fatalf("search_posts error = %v", err)
	}
	if all["n_returned"] != float64(1) {
		t.Fatalf("case-insensitive search = %#v", all)
	}

	// Target-agent references resolve relative to the caller's path.
	subscription, err := boardToolBody(t, execs["subscribe"], boardInvocation(t, "subscribe", map[string]any{"channel_name": "work", "target_agent": "worker"}))
	if err != nil {
		t.Fatalf("subscribe error = %v", err)
	}
	if subscription["target_agent"] != "/root/worker" || subscription["enabled"] != true {
		t.Fatalf("subscribe body = %#v", subscription)
	}
	unsubscribed, err := boardToolBody(t, execs["unsubscribe"], boardInvocation(t, "unsubscribe", map[string]any{"channel_name": "work", "target_agent": "/root/worker"}))
	if err != nil {
		t.Fatalf("unsubscribe error = %v", err)
	}
	if unsubscribed["enabled"] != false {
		t.Fatalf("unsubscribe body = %#v", unsubscribed)
	}

	// The child can post to the channel and is subscribed to its own thread.
	childExecs := boardToolSet(t, board, child, agent.AgentPath("/root/worker"))
	childPost, err := boardToolBody(t, childExecs["post"], boardInvocation(t, "post", map[string]any{"channel_name": "work", "text": "hi"}))
	if err != nil {
		t.Fatalf("child post error = %v", err)
	}
	if childPost["author"] != "/root/worker" {
		t.Fatalf("child post = %#v", childPost)
	}
}

func TestMessageBoardToolsRejectInvalidArgumentsLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	boards := &InMemoryMessageBoards{}
	board := boards.Open(root, host)
	defer board.Close()
	execs := boardToolSet(t, board, root, agent.AgentPathRoot)
	validThreadID := "6b1c4f2a-0000-4000-8000-000000000001"

	tests := []struct {
		tool  string
		args  string
		parts []string
	}{
		{"create_channel", `{"channel_name":"work","unknown":1}`, []string{"unknown"}},
		{"create_channel", `{}`, []string{"missing field `channel_name`"}},
		{"get_channels", `{"limit":0}`, []string{"limit", "nonzero u32"}},
		{"get_channels", `{"limit":-3}`, []string{"limit", "nonzero u32"}},
		{"list_threads", `{"channel_name":"work","sort":"recent"}`, []string{"unknown variant `recent`", "`created` or `activity`"}},
		{"list_threads", `{"channel_name":"work","max_chars_per_post":0}`, []string{"max_chars_per_post", "nonzero u32"}},
		{"read_thread", `{"thread_id":"not-a-uuid"}`, []string{"thread_id", "expected a UUID"}},
		{"read_thread", `{}`, []string{"missing field `thread_id`"}},
		{"read_post", `{"message_id":"not-a-uuid"}`, []string{"message_id", "expected a UUID"}},
		{"read_post", `{"message_id":"` + validThreadID + `","offset_chars":-2}`, []string{"offset_chars", "expected u32"}},
		{"read_post", `{"message_id":"` + validThreadID + `","limit_chars":0}`, []string{"limit_chars", "nonzero u32"}},
		{"search_posts", `{"after_message_id":"nope"}`, []string{"after_message_id", "expected a UUID"}},
		{"subscribe", `{"channel_name":"work","thread_id":"` + validThreadID + `"}`, []string{"exactly one of channel_name or thread_id"}},
		{"subscribe", `{"target_agent":"worker"}`, []string{"exactly one of channel_name or thread_id"}},
		{"post", `{}`, []string{"missing field `text`"}},
		{"post", `{"text":"hi"}`, []string{"exactly one of channel_name, new_channel_name or thread_id"}},
		{"post", `{"text":"hi","channel_name":"work","thread_id":"` + validThreadID + `"}`, []string{"exactly one of channel_name, new_channel_name or thread_id"}},
		{"post", `{"text":"hi","channel_name":"work","author":"x"}`, []string{"unknown field"}},
		{"post", `{"text":"hi","new_channel_name":"work","agents_to_notify":[""]}`, []string{"agent path must not be empty"}},
	}

	for _, test := range tests {
		executor, ok := execs[test.tool]
		if !ok {
			t.Fatalf("missing tool %s", test.tool)
		}
		_, err := boardToolBody(t, executor, boardInvocationRaw(test.tool, test.args))
		if err == nil {
			t.Fatalf("%s(%s) succeeded, want a model error", test.tool, test.args)
		}
		var callErr *tool.FunctionCallError
		if !tool.AsFunctionCallError(err, &callErr) || !callErr.RespondsToModel() {
			t.Fatalf("%s(%s) error = %v, want a responder error", test.tool, test.args, err)
		}
		for _, part := range test.parts {
			if !strings.Contains(callErr.ModelMessage(), part) {
				t.Fatalf("%s(%s) message = %q, want it to contain %q", test.tool, test.args, callErr.ModelMessage(), part)
			}
		}
	}

	// A failed mutation must not have changed the board.
	channels, err := boardToolBody(t, execs["get_channels"], boardInvocation(t, "get_channels", map[string]any{}))
	if err != nil {
		t.Fatalf("get_channels error = %v", err)
	}
	if channels["n_returned"] != float64(0) {
		t.Fatalf("board changed after rejected calls: %#v", channels)
	}
}

func TestMessageBoardToolsBoundResultsAndMutationsLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	boards := &InMemoryMessageBoards{}
	board := boards.Open(root, host)
	defer board.Close()
	execs := boardToolSet(t, board, root, agent.AgentPathRoot)

	// 30 long channel names make the default page far larger than a small
	// budget, forcing bounded_read to halve the page limit.
	for index := 0; index < 30; index++ {
		name := "channel-" + strings.Repeat("x", 110) + "-" + string(rune('a'+index%26)) + string(rune('a'+index/26))
		if _, err := boardToolBody(t, execs["create_channel"], boardInvocation(t, "create_channel", map[string]any{"channel_name": name})); err != nil {
			t.Fatalf("create_channel(%d) error = %v", index, err)
		}
	}

	full, err := boardToolBody(t, execs["get_channels"], boardInvocation(t, "get_channels", map[string]any{"limit": 20}))
	if err != nil {
		t.Fatalf("get_channels error = %v", err)
	}
	if full["n_returned"] != float64(20) || full["has_more"] != true {
		t.Fatalf("full page = %#v", full)
	}

	// A direct call whose model allowance is small must return a smaller page
	// that still fits the budget.
	small := boardInvocation(t, "get_channels", map[string]any{"limit": 20})
	small.Truncation = &utils.TruncationPolicy{Mode: utils.PolicyBytes, Limit: 2000}
	budget := small.ResponseByteBudget(maxMessageBoardResponseBytes)
	if budget >= maxMessageBoardResponseBytes {
		t.Fatalf("budget = %d, want the model allowance to bind", budget)
	}
	bounded, err := boardToolBody(t, execs["get_channels"], small)
	if err != nil {
		t.Fatalf("bounded get_channels error = %v", err)
	}
	count, _ := bounded["n_returned"].(float64)
	if count >= 20 || count < 1 {
		t.Fatalf("bounded page returned %v of 20", bounded["n_returned"])
	}
	encoded, err := json.Marshal(bounded)
	if err != nil {
		t.Fatalf("marshal bounded page: %v", err)
	}
	if len(encoded) > budget {
		t.Fatalf("bounded page is %d bytes, budget %d", len(encoded), budget)
	}

	// Code Mode receives typed results, so only the tool limit applies.
	codeMode := boardInvocation(t, "get_channels", map[string]any{"limit": 20})
	codeMode.Source = tool.InvocationSourceCodeMode
	codeMode.Truncation = &utils.TruncationPolicy{Mode: utils.PolicyBytes, Limit: 2000}
	if codeMode.ResponseByteBudget(maxMessageBoardResponseBytes) != maxMessageBoardResponseBytes {
		t.Fatalf("code-mode budget = %d, want %d", codeMode.ResponseByteBudget(maxMessageBoardResponseBytes), maxMessageBoardResponseBytes)
	}

	// A mutation whose acknowledgement cannot fit the budget fails without
	// changing the board.
	tiny := boardInvocation(t, "create_channel", map[string]any{"channel_name": "too-small"})
	tiny.Truncation = &utils.TruncationPolicy{Mode: utils.PolicyBytes, Limit: 100}
	_, err = boardToolBody(t, execs["create_channel"], tiny)
	if err == nil {
		t.Fatal("create_channel with a tiny budget succeeded")
	}
	var callErr *tool.FunctionCallError
	if !tool.AsFunctionCallError(err, &callErr) || !strings.Contains(callErr.ModelMessage(), "Output budget is too small to acknowledge a message-board mutation") {
		t.Fatalf("mutation budget error = %v", err)
	}
	after, err := boardToolBody(t, execs["get_channels"], boardInvocation(t, "get_channels", map[string]any{"query": "too-small"}))
	if err != nil {
		t.Fatalf("get_channels error = %v", err)
	}
	if after["n_returned"] != float64(0) {
		t.Fatalf("rejected mutation changed the board: %#v", after)
	}
}

func TestMessageBoardRequestIDsLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	boards := &InMemoryMessageBoards{}
	board := boards.Open(root, host)
	defer board.Close()
	execs := boardToolSet(t, board, root, agent.AgentPathRoot)

	first := boardInvocation(t, "post", map[string]any{"new_channel_name": "work", "text": "hello"})
	first.CallID = "call-1"
	posted, err := boardToolBody(t, execs["post"], first)
	if err != nil {
		t.Fatalf("post error = %v", err)
	}
	// The same invocation identity is idempotent.
	retry, err := boardToolBody(t, execs["post"], first)
	if err != nil {
		t.Fatalf("post retry error = %v", err)
	}
	if retry["message_id"] != posted["message_id"] {
		t.Fatalf("retry created a new post: %#v vs %#v", retry, posted)
	}
	// A reused request ID with different input is rejected.
	changed := boardInvocation(t, "post", map[string]any{"new_channel_name": "work", "text": "different"})
	changed.CallID = "call-1"
	if _, err := boardToolBody(t, execs["post"], changed); err == nil {
		t.Fatal("a reused request ID with different input succeeded")
	}

	// A Code Mode call derives a different request ID from the cell and the
	// runtime call ID, so the same runtime call ID can post again.
	codeMode := boardInvocation(t, "post", map[string]any{"channel_name": "work", "text": "hello"})
	codeMode.CallID = "call-1"
	codeMode.Source = tool.InvocationSourceCodeMode
	codeMode.Context[tool.CodeModeCellIDContextKey] = "cell-1"
	postedFromCell, err := boardToolBody(t, execs["post"], codeMode)
	if err != nil {
		t.Fatalf("code-mode post error = %v", err)
	}
	if postedFromCell["message_id"] == posted["message_id"] {
		t.Fatalf("code-mode call reused the direct request ID: %#v", postedFromCell)
	}
}

// Rust returns every board result as
// `JsonToolOutput::new(result).with_external_context()`, which the host consumes
// to mark the thread's memory mode polluted.
func TestMessageBoardOutputsDeclareExternalContextLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	boards := &InMemoryMessageBoards{}
	board := boards.Open(root, host)
	defer board.Close()
	executors := NewMessageBoardTools(board, root, agent.AgentPathRoot, "", "")
	output, err := boardToolExecutor(t, executors, "get_channels").Execute(context.Background(), boardInvocation(t, "get_channels", map[string]any{}))
	if err != nil {
		t.Fatalf("get_channels error = %v", err)
	}
	if output == nil || !output.ContainsExternalContext {
		t.Fatalf("get_channels output = %#v, want the external-context marker", output)
	}
}

func boardToolExecutor(t *testing.T, executors []tool.Executor, name string) tool.Executor {
	t.Helper()
	for _, executor := range executors {
		if executor.Spec().Name.Name == name {
			return executor
		}
	}
	t.Fatalf("missing tool %s", name)
	return nil
}

func boardToolSet(t *testing.T, board Board, caller string, callerPath agent.AgentPath) map[string]tool.Executor {
	t.Helper()
	executors := NewMessageBoardTools(board, caller, callerPath, "", "")
	if len(executors) != len(MessageBoardToolNames) {
		t.Fatalf("built %d tools, want %d", len(executors), len(MessageBoardToolNames))
	}
	byName := make(map[string]tool.Executor, len(executors))
	for _, executor := range executors {
		byName[executor.Spec().Name.Name] = executor
	}
	return byName
}

func boardToolBody(t *testing.T, executor tool.Executor, invocation *tool.Invocation) (map[string]any, error) {
	t.Helper()
	output, err := executor.Execute(context.Background(), invocation)
	if err != nil {
		return nil, err
	}
	if output == nil || !output.Success {
		t.Fatalf("output = %#v", output)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output.Body), &decoded); err != nil {
		t.Fatalf("decode output body %q: %v", output.Body, err)
	}
	return decoded, nil
}

func boardInvocation(t *testing.T, name string, args any) *tool.Invocation {
	t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return boardInvocationRaw(name, string(encoded))
}

// boardToolCallSeq gives every helper-built invocation its own call ID so two
// calls in one test are not mistaken for a retry of the same request.
var boardToolCallSeq int

func boardInvocationRaw(name string, args string) *tool.Invocation {
	boardToolCallSeq++
	return &tool.Invocation{
		CallID:   fmt.Sprintf("call-%d", boardToolCallSeq),
		ToolName: tool.PlainName(name),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: args},
		Context:  map[string]any{"turn_id": "turn-1"},
	}
}
