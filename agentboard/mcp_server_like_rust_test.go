package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codex_go/agent"
)

// Rust parity: codex-rs/message-board/tests/local_board/mcp_tests.rs (#58341).
func mcpTestBoard(t *testing.T, backend string) (Board, string) {
	t.Helper()
	host, caller, _, _ := testTree(t)
	if backend == "memory" {
		board := (&InMemoryMessageBoards{}).Open(caller, host)
		t.Cleanup(board.Close)
		return board, caller
	}
	board := openLocalBoard(t, openBoardSqliteConfig(t), caller, host)
	t.Cleanup(board.Close)
	return board, caller
}

func TestMessageBoardMCPServerDiscoveryAndCallsLikeRust(t *testing.T) {
	ctx := context.Background()
	for _, backend := range []string{"memory", "local"} {
		t.Run(backend, func(t *testing.T) {
			board, caller := mcpTestBoard(t, backend)
			server := NewMessageBoardMCPServer(MessageBoardDiscussionTools, board, caller, agent.AgentPathRoot)

			tools, err := server.ListTools(nil)
			if err != nil {
				t.Fatalf("ListTools() error = %v", err)
			}
			names := make([]string, 0, len(tools))
			for _, entry := range tools {
				names = append(names, entry.Name)
				if entry.Description == "" {
					t.Fatalf("%s has no description", entry.Name)
				}
				if entry.InputSchema["type"] != "object" {
					t.Fatalf("%s schema = %#v", entry.Name, entry.InputSchema)
				}
			}
			if len(names) != len(MessageBoardDiscussionTools) {
				t.Fatalf("tools = %v, want %d discussion tools", names, len(MessageBoardDiscussionTools))
			}
			for index, selected := range MessageBoardDiscussionTools {
				if names[index] != string(selected) {
					t.Fatalf("tools[%d] = %q, want %q", index, names[index], selected)
				}
			}
			cursor := "next"
			var protocolErr *MessageBoardMCPError
			if _, err := server.ListTools(&cursor); !errors.As(err, &protocolErr) || protocolErr.Code != MessageBoardMCPInvalidParamsCode {
				t.Fatalf("ListTools(cursor) error = %v, want invalid params", err)
			}

			text := strings.Repeat("é\"", 6_000)
			posted, err := server.CallTool(ctx, "post", map[string]any{"new_channel_name": "work", "text": text}, 1)
			if err != nil {
				t.Fatalf("CallTool(post) error = %v", err)
			}
			if posted.IsError {
				t.Fatalf("post result = %#v", posted)
			}
			if posted.StructuredContent["author"] != string(agent.AgentPathRoot) {
				t.Fatalf("post author = %#v", posted.StructuredContent["author"])
			}
			messageID, _ := posted.StructuredContent["message_id"].(string)
			if messageID == "" {
				t.Fatalf("post result = %#v", posted)
			}

			read, err := server.CallTool(ctx, "read_post", map[string]any{"message_id": messageID}, 2)
			if err != nil {
				t.Fatalf("CallTool(read_post) error = %v", err)
			}
			if read.IsError || read.StructuredContent == nil {
				t.Fatalf("read_post result = %#v", read)
			}
			if len(read.Content) != 1 || len(read.Content[0].Text) > maxMessageBoardResponseBytes {
				t.Fatalf("read_post content bytes = %d", len(read.Content[0].Text))
			}
			nChars := int(read.StructuredContent["n_chars"].(float64))
			nextOffset := int(read.StructuredContent["next_offset_chars"].(float64))
			if nextOffset >= nChars {
				t.Fatalf("next_offset_chars = %d, n_chars = %d", nextOffset, nChars)
			}
			if got, want := read.StructuredContent["text"], string([]rune(text)[:nextOffset]); got != want {
				t.Fatalf("read text is not the requested prefix: %d chars vs %d", len(got.(string)), len(want))
			}

			// Identity is bound by the host, never supplied in model arguments.
			spoofed, err := server.CallTool(ctx, "post", map[string]any{
				"new_channel_name": "spoofed", "text": "bad", "caller": "other-thread",
			}, 3)
			if err != nil {
				t.Fatalf("spoofed post returned a protocol error: %v", err)
			}
			if !spoofed.IsError {
				t.Fatalf("spoofed post result = %#v", spoofed)
			}
			query := "spoofed"
			channels, err := board.ListChannels(ctx, caller, ChannelQuery{Query: &query, Direction: NewestFirst})
			if err != nil {
				t.Fatalf("ListChannels() error = %v", err)
			}
			if len(channels.Page.Results) != 0 {
				t.Fatalf("spoofed channel was created: %#v", channels.Page.Results)
			}

			if _, err := server.CallTool(ctx, "unknown", nil, 4); !errors.As(err, &protocolErr) || protocolErr.Code != MessageBoardMCPInvalidParamsCode {
				t.Fatalf("CallTool(unknown) error = %v, want invalid params", err)
			}

			// upsert_channel returns null, which legacy MCP cannot express as
			// structured content, so only the JSON text is returned.
			upsert := NewMessageBoardMCPServer([]MessageBoardTool{MessageBoardToolUpsertChannel}, board, caller, agent.AgentPathRoot)
			upserted, err := upsert.CallTool(ctx, "upsert_channel", map[string]any{"channel_name": "work", "description": "updated"}, 5)
			if err != nil {
				t.Fatalf("CallTool(upsert_channel) error = %v", err)
			}
			if upserted.IsError || upserted.StructuredContent != nil || upserted.Content[0].Text != "null" {
				t.Fatalf("upsert_channel result = %#v", upserted)
			}
		})
	}
}

func TestMessageBoardMCPServerDeduplicatesPerSessionLikeRust(t *testing.T) {
	ctx := context.Background()
	host, caller, _, _ := testTree(t)
	board := (&InMemoryMessageBoards{}).Open(caller, host)
	defer board.Close()
	if _, err := board.CreateChannel(ctx, caller, CreateChannelRequest{ChannelName: "general", Subscription: Unsubscribe}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}

	results := make([]*MessageBoardMCPToolResult, 0, 4)
	for session := 0; session < 2; session++ {
		server := NewMessageBoardMCPServer([]MessageBoardTool{MessageBoardToolPost}, board, caller, agent.AgentPathRoot)
		for _, requestID := range []any{7, "7"} {
			arguments := map[string]any{"channel_name": "general", "text": "same request"}
			first, err := server.CallTool(ctx, "post", arguments, requestID)
			if err != nil {
				t.Fatalf("CallTool(post, %v) error = %v", requestID, err)
			}
			retry, err := server.CallTool(ctx, "post", arguments, requestID)
			if err != nil {
				t.Fatalf("CallTool(post retry, %v) error = %v", requestID, err)
			}
			firstJSON, _ := json.Marshal(first)
			retryJSON, _ := json.Marshal(retry)
			if string(firstJSON) != string(retryJSON) {
				t.Fatalf("retry result = %s, want %s", retryJSON, firstJSON)
			}
			results = append(results, first)
		}
	}
	for index, result := range results {
		for previous := 0; previous < index; previous++ {
			if result.StructuredContent["message_id"] == results[previous].StructuredContent["message_id"] {
				t.Fatalf("post %d reused message %v", index, result.StructuredContent["message_id"])
			}
		}
	}
	channel := "general"
	query := "same request"
	page, err := board.SearchPosts(ctx, caller, PostQuery{ChannelName: &channel, Query: &query, MaxCharsPerPost: 100})
	if err != nil {
		t.Fatalf("SearchPosts() error = %v", err)
	}
	if len(page.Results) != 4 {
		t.Fatalf("stored posts = %d, want 4", len(page.Results))
	}
}

func TestMessageBoardMCPServerReadOnlyConnectionDeniesLikeRust(t *testing.T) {
	ctx := context.Background()
	host, caller, _, _ := testTree(t)
	board, err := NewLocalBoard(openBoardSqliteConfig(t), caller).ReadOnly().Open(ctx, host)
	if err != nil {
		t.Fatalf("Open(read-only) error = %v", err)
	}
	defer board.Close()

	server := NewMessageBoardMCPServer(MessageBoardDiscussionTools, board, caller, agent.AgentPathRoot)
	result, err := server.CallTool(ctx, "post", map[string]any{"new_channel_name": "blocked", "text": "bad"}, 1)
	if err != nil {
		t.Fatalf("permission denial returned a protocol error: %v", err)
	}
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("denied result = %#v", result)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &body); err != nil {
		t.Fatalf("Unmarshal denial error = %v", err)
	}
	failure, _ := body["error"].(map[string]any)
	if failure["code"] != "permission_denied" || failure["action"] != "channel.post" || failure["resource"] != "board:"+caller {
		t.Fatalf("structured denial = %#v", body)
	}
	if result.StructuredContent == nil || result.StructuredContent["error"] == nil {
		t.Fatalf("denial structured content = %#v", result.StructuredContent)
	}
}
