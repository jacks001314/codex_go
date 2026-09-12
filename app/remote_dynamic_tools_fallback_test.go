package app

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// TestRemoteStartThreadRetriesWithoutDynamicToolsLikeRust mirrors Rust
// request_thread_start_with_history_fallback's dynamic-tools downgrade: a server
// that rejects the TUI's task-tool namespace starts the thread without it, and
// the thread is then reported as not hosting task tools.
func TestRemoteStartThreadRetriesWithoutDynamicToolsLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientConn, serverConn := net.Pipe()
	var mu sync.Mutex
	var dynamicToolAttempts []bool
	go func() {
		defer serverConn.Close()
		decoder := json.NewDecoder(serverConn)
		encoder := json.NewEncoder(serverConn)
		for {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := decoder.Decode(&request); err != nil {
				return
			}
			switch request.Method {
			case string(appserver.MethodThreadStart):
				var params map[string]any
				_ = json.Unmarshal(request.Params, &params)
				_, hasDynamicTools := params["dynamicTools"]
				mu.Lock()
				dynamicToolAttempts = append(dynamicToolAttempts, hasDynamicTools)
				attempt := len(dynamicToolAttempts)
				mu.Unlock()
				if attempt == 1 {
					_ = encoder.Encode(map[string]any{
						"jsonrpc": "2.0",
						"id":      request.ID,
						"error":   map[string]any{"code": -32600, "message": "Invalid request: unknown field `dynamicTools`"},
					})
					continue
				}
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      request.ID,
					"result":  map[string]any{"thread": map[string]any{"id": "thread-retry"}},
				})
			default:
				_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
			}
		}
	}()

	state := codextui.NewState(nil)
	messages := make(chan bubbletea.Msg, 32)
	client := &remoteAppServerTUIClient{
		endpoint: appserverdaemon.NewUnixSocketEndpoint("/tmp/codex-dynamic-tools.sock"),
		state:    state,
		messages: messages,
		unixDial: func(context.Context, string) (net.Conn, error) { return clientConn, nil },
	}
	if err := client.connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	threadID, err := client.startThread(ctx, &cli.RootOptions{}, state)
	if err != nil {
		t.Fatalf("startThread: %v", err)
	}
	if threadID != "thread-retry" {
		t.Fatalf("threadID = %q", threadID)
	}
	mu.Lock()
	attempts := append([]bool(nil), dynamicToolAttempts...)
	mu.Unlock()
	if len(attempts) != 2 || !attempts[0] || attempts[1] {
		t.Fatalf("dynamic tool attempts = %#v", attempts)
	}
	capability, ok := drainTaskToolsAvailable(messages, "thread-retry")
	if !ok {
		t.Fatal("task-tool capability was not reported")
	}
	if capability.Available {
		t.Fatalf("capability = %#v, want unavailable after the downgrade", capability)
	}
}

// drainTaskToolsAvailable returns the last capability report for a thread.
func drainTaskToolsAvailable(messages chan bubbletea.Msg, threadID string) (codextea.TaskToolsAvailableMsg, bool) {
	found := false
	var capability codextea.TaskToolsAvailableMsg
	for {
		select {
		case message := <-messages:
			if available, ok := message.(codextea.TaskToolsAvailableMsg); ok && available.ThreadID == threadID {
				found = true
				capability = available
			}
			continue
		default:
			return capability, found
		}
	}
}
