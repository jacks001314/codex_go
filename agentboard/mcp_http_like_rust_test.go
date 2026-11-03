package agentboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/agent"
)

func newMessageBoardHTTPTestServer(t *testing.T, binder MessageBoardMCPBind) (*MessageBoardMCPHTTP, string, string) {
	t.Helper()
	const token = "test-token"
	server, err := NewMessageBoardMCPHTTP(MessageBoardMCPHTTPConfig{Token: token, Bind: binder})
	if err != nil {
		t.Fatalf("NewMessageBoardMCPHTTP() error = %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	endpoint, err := server.Serve(listener)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	return server, endpoint, token
}

func postMCPJSON(t *testing.T, endpoint, token, sessionID string, body string) (*http.Response, mcpRPCResponse) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		request.Header.Set(MessageBoardMCPSessionHeader, sessionID)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer response.Body.Close()
	var decoded mcpRPCResponse
	_ = json.NewDecoder(response.Body).Decode(&decoded)
	return response, decoded
}

func initializeMCPHTTP(t *testing.T, endpoint, token string) string {
	t.Helper()
	response, decoded := postMCPJSON(t, endpoint, token, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if response.StatusCode != http.StatusOK || decoded.Error != nil {
		t.Fatalf("initialize = %d %#v", response.StatusCode, decoded)
	}
	sessionID := response.Header.Get(MessageBoardMCPSessionHeader)
	if sessionID == "" {
		t.Fatal("initialize did not issue an MCP session id")
	}
	return sessionID
}

func messageBoardHTTPTestBinding(t *testing.T) (MessageBoardMCPBind, *InMemoryBoard) {
	t.Helper()
	host := newTestHost(t, map[string]agent.AgentPath{"thread-1": agent.AgentPathRoot})
	boards := &InMemoryMessageBoards{}
	board := boards.Open("tree", host)
	bind := func(ctx context.Context, threadID, sessionID string) (*MessageBoardMCPServer, error) {
		if threadID != "thread-1" {
			return nil, errors.New("caller is not a member of this session")
		}
		path, err := host.AgentPath(ctx, threadID)
		if err != nil {
			return nil, err
		}
		return NewMessageBoardMCPServer(MessageBoardDiscussionTools, board, threadID, path), nil
	}
	return bind, board
}

// Rust #58904: the bearer authorizes a trusted host, and foreign Host headers
// are refused before any MCP processing.
func TestMessageBoardMCPHTTPRequiresBearerAndHostLikeRust(t *testing.T) {
	bind, board := messageBoardHTTPTestBinding(t)
	defer board.Close()
	_, endpoint, token := newMessageBoardHTTPTestServer(t, bind)
	response, _ := postMCPJSON(t, endpoint, "wrong-token", "", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", response.StatusCode)
	}
	request, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Host = "evil.example"
	forged, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	forged.Body.Close()
	if forged.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign host status = %d", forged.StatusCode)
	}
}

// Rust #58904: discussion tools are listed and callable through the MCP session,
// and calls require the host-asserted caller metadata.
func TestMessageBoardMCPHTTPListsAndCallsDiscussionToolsLikeRust(t *testing.T) {
	bind, board := messageBoardHTTPTestBinding(t)
	defer board.Close()
	_, endpoint, token := newMessageBoardHTTPTestServer(t, bind)
	sessionID := initializeMCPHTTP(t, endpoint, token)
	response, decoded := postMCPJSON(t, endpoint, token, sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if response.StatusCode != http.StatusOK || decoded.Error != nil {
		t.Fatalf("tools/list = %d %#v", response.StatusCode, decoded)
	}
	encoded, _ := json.Marshal(decoded.Result)
	if !strings.Contains(string(encoded), `"post"`) || !strings.Contains(string(encoded), `"read_thread"`) {
		t.Fatalf("tools/list result = %s", encoded)
	}
	missing, decodedMissing := postMCPJSON(t, endpoint, token, sessionID, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"post","arguments":{"text":"hi","new_channel_name":"general"}}}`)
	if missing.StatusCode != http.StatusOK || decodedMissing.Error == nil || !strings.Contains(decodedMissing.Error.Message, "threadId and sessionId") {
		t.Fatalf("call without metadata = %d %#v", missing.StatusCode, decodedMissing)
	}
	called, decodedCalled := postMCPJSON(t, endpoint, token, sessionID, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"post","arguments":{"text":"hello board","new_channel_name":"general"},"_meta":{"threadId":"thread-1","sessionId":"thread-1"}}}`)
	if called.StatusCode != http.StatusOK || decodedCalled.Error != nil {
		t.Fatalf("tools/call = %d %#v", called.StatusCode, decodedCalled)
	}
	encodedResult, _ := json.Marshal(decodedCalled.Result)
	if !strings.Contains(string(encodedResult), `"channel_name":"general"`) || !strings.Contains(string(encodedResult), `"isError":false`) {
		t.Fatalf("tools/call result = %s", encodedResult)
	}
	invalid, decodedInvalid := postMCPJSON(t, endpoint, token, sessionID, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"post","arguments":{"text":"x","new_channel_name":"general"},"_meta":{"threadId":"intruder","sessionId":"thread-1"}}}`)
	if invalid.StatusCode != http.StatusOK || decodedInvalid.Error == nil || !strings.Contains(decodedInvalid.Error.Message, "not a member") {
		t.Fatalf("invalid membership = %d %#v", invalid.StatusCode, decodedInvalid)
	}
}

// Rust #58904: shutdown waits for already-dispatched bindings before it
// releases the MCP state.
func TestMessageBoardMCPHTTPShutdownDrainsBindingsLikeRust(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	bind := func(ctx context.Context, threadID, sessionID string) (*MessageBoardMCPServer, error) {
		once.Do(func() { close(started) })
		<-release
		return nil, errors.New("released")
	}
	server, endpoint, token := newMessageBoardHTTPTestServer(t, bind)
	sessionID := initializeMCPHTTP(t, endpoint, token)
	go func() {
		request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"post","arguments":{"text":"x","new_channel_name":"general"},"_meta":{"threadId":"thread-1","sessionId":"thread-1"}}}`))
		if err != nil {
			return
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(MessageBoardMCPSessionHeader, sessionID)
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the call never reached the binder")
	}
	closed := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		closed <- server.Close(ctx)
	}()
	select {
	case <-closed:
		t.Fatal("shutdown returned before the binding released")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish after the binding released")
	}
}
