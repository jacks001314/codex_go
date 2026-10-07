package execserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// execServerWireRequest is a minimal JSON-RPC request envelope the test server
// records so the assertion can prove which methods actually reached the
// executor. Notifications carry no id.
type execServerWireRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Rust #49805 `e7798c9944`: writable file streams are capability-gated on the
// exec-server client (codex-rs/exec-server/src/client.rs, test
// `writable_file_streams_require_executor_capability`). An executor that did
// not advertise `file_write_streaming` must never receive a replacement open or
// a block write — it could silently treat a writable open as a read — while
// read-only opens still reach it.
func TestWritableFileStreamsRequireExecutorCapabilityLikeRust(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	var openModes []string
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for {
			_, payload, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var request execServerWireRequest
			if err := json.Unmarshal(payload, &request); err != nil {
				serverErr <- err
				return
			}
			if request.Method == "" {
				continue // a response or notification echoed by the client
			}
			if len(request.ID) == 0 {
				continue // the `initialized` notification carries no id
			}
			mu.Lock()
			methods = append(methods, request.Method)
			if request.Method == MethodFSOpen {
				var params struct {
					Mode string `json:"mode"`
				}
				if err := json.Unmarshal(request.Params, &params); err != nil {
					mu.Unlock()
					serverErr <- err
					return
				}
				openModes = append(openModes, params.Mode)
			}
			mu.Unlock()
			var result any
			switch request.Method {
			case MethodInitialize:
				info := EnvironmentInfo{
					Shell:        ShellInfo{Name: "bash", Path: "/bin/bash"},
					Capabilities: EnvironmentCapabilities{FileWriteStreaming: false},
				}
				result = InitializeResponse{SessionID: "file-stream-session", EnvironmentInfo: &info}
			case MethodFSOpen:
				result = FSOpenResponse{HandleID: "read-handle"}
			default:
				result = map[string]any{}
			}
			encoded, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result":  result,
			})
			if err != nil {
				serverErr <- err
				return
			}
			if err := conn.Write(ctx, websocket.MessageText, encoded); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := DialClient(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), "file-stream-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	defer client.Close()

	// A replacement open is rejected locally before it reaches the executor.
	if _, err := client.FSOpen(ctx, &FSOpenParams{
		HandleID: "write-handle",
		Path:     "file:///tmp/existing.txt",
		Mode:     fsOpenModeReplace,
	}); err == nil || !strings.Contains(err.Error(), "does not support writable file streams") {
		t.Fatalf("replace FSOpen() error = %v, want the writable-file-streams protocol error", err)
	}

	// Block writes are rejected locally before they reach the executor.
	if _, err := client.FSWriteBlock(ctx, &FSWriteBlockParams{
		HandleID: "write-handle",
		Offset:   0,
		Chunk:    "AQ==",
	}); err == nil || !strings.Contains(err.Error(), "does not support writable file streams") {
		t.Fatalf("FSWriteBlock() error = %v, want the writable-file-streams protocol error", err)
	}

	// A read-only open is still forwarded and succeeds.
	response, err := client.FSOpen(ctx, &FSOpenParams{
		HandleID: "read-handle",
		Path:     "file:///tmp/existing.txt",
		Mode:     fsOpenModeRead,
	})
	if err != nil {
		t.Fatalf("read-only FSOpen() error = %v", err)
	}
	if response == nil || response.HandleID != "read-handle" {
		t.Fatalf("read-only FSOpen() response = %#v", response)
	}

	mu.Lock()
	gotMethods := append([]string(nil), methods...)
	gotModes := append([]string(nil), openModes...)
	mu.Unlock()
	want := []string{MethodInitialize, MethodFSOpen}
	if strings.Join(gotMethods, ",") != strings.Join(want, ",") {
		t.Fatalf("executor saw methods %v, want exactly %v (unsupported writable requests must be rejected locally)", gotMethods, want)
	}
	if len(gotModes) != 1 || gotModes[0] != fsOpenModeRead {
		t.Fatalf("executor saw fs/open modes %v, want exactly [read]", gotModes)
	}

	select {
	case err := <-serverErr:
		t.Fatalf("scripted executor error = %v", err)
	default:
	}
}
