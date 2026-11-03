package agentboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
)

func callInMemoryMCP(t *testing.T, endpoint, authorization, sessionID, body string) mcpRPCResponse {
	t.Helper()
	response, decoded := postMCPJSON(t, endpoint, strings.TrimPrefix(authorization, "Bearer "), sessionID, body)
	if response.StatusCode != 200 {
		t.Fatalf("status = %d", response.StatusCode)
	}
	return decoded
}

// Rust #58909: a temporary loopback endpoint shares one board across the parent
// and child agents of a registered session without a SQLite board database.
func TestInMemoryMessageBoardMCPSharesBoardAcrossTreeLikeRust(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	launcher, err := LaunchInMemoryMessageBoardMCP(ctx, InMemoryMessageBoardMCPOptions{})
	if err != nil {
		t.Fatalf("LaunchInMemoryMessageBoardMCP() error = %v", err)
	}
	host := newTestHost(t, map[string]agent.AgentPath{
		"thread-parent": agent.AgentPathRoot,
		"thread-child":  agent.AgentPath("/root/worker"),
	})
	if err := launcher.AddBoard("session-1", host); err != nil {
		t.Fatalf("AddBoard() error = %v", err)
	}
	response, decoded := postMCPJSON(t, launcher.Endpoint(), strings.TrimPrefix(launcher.AuthorizationHeader(), "Bearer "), "", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if response.StatusCode != 200 || decoded.Error != nil {
		t.Fatalf("initialize = %d %#v", response.StatusCode, decoded)
	}
	sessionID := response.Header.Get(MessageBoardMCPSessionHeader)
	posted := callInMemoryMCP(t, launcher.Endpoint(), launcher.AuthorizationHeader(), sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"post","arguments":{"text":"parent decision","new_channel_name":"general"},"_meta":{"threadId":"thread-parent","sessionId":"session-1"}}}`)
	if posted.Error != nil {
		t.Fatalf("parent post error = %#v", posted.Error)
	}
	read := callInMemoryMCP(t, launcher.Endpoint(), launcher.AuthorizationHeader(), sessionID, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_posts","arguments":{"channel_name":"general"},"_meta":{"threadId":"thread-child","sessionId":"session-1"}}}`)
	if read.Error != nil {
		t.Fatalf("child read error = %#v", read.Error)
	}
	encoded, _ := json.Marshal(read.Result)
	if !strings.Contains(string(encoded), "parent decision") {
		t.Fatalf("child did not see the shared board: %s", encoded)
	}
	unknown := callInMemoryMCP(t, launcher.Endpoint(), launcher.AuthorizationHeader(), sessionID, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"post","arguments":{"text":"x","new_channel_name":"general"},"_meta":{"threadId":"thread-parent","sessionId":"missing-session"}}}`)
	if unknown.Error == nil || !strings.Contains(unknown.Error.Message, "not a member") {
		t.Fatalf("unregistered session = %#v", unknown.Error)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := launcher.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if launcher.boards.Len() != 0 {
		t.Fatalf("launcher retained %d boards after shutdown", launcher.boards.Len())
	}
}
