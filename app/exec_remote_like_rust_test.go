package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/turn"
)

// TestRemoteExecRunsNewThreadLikeRust covers Rust #61707: `codex exec --remote`
// starts a thread on the server, streams the turn, and writes the JSONL and
// last-message outputs. The endpoint arrives through the root options to
// exercise the exec merge.
func TestRemoteExecRunsNewThreadLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 16)
	serverErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				if errors.Is(err, context.Canceled) || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			requests <- req
			switch req.Method {
			case string(appserver.MethodInitialize):
				var params appserver.InitializeParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ClientInfo.Name != "codex_exec" || params.Capabilities == nil || params.Capabilities.RequiredHumanApproval {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("exec initialize = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadStart):
				var params appserver.ThreadStartParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ApprovalPolicy != "never" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("thread/start approvalPolicy = %#v", params.ApprovalPolicy))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"thread": map[string]any{"id": "thread-remote", "status": map[string]any{"type": "idle"}},
				}})
			case string(appserver.MethodTurnStart):
				var params turn.TurnStartParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-remote" || params.TurnTrigger != "exec" || params.ApprovalPolicy != "never" ||
					len(params.Input) != 1 || params.Input[0].Text != "hello" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("turn/start params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"turn": map[string]any{"id": "turn-remote", "items": []any{}, "status": "inProgress"},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationTurnStarted), "params": map[string]any{
					"threadId": "thread-remote", "turn": map[string]any{"id": "turn-remote", "items": []any{}, "status": "inProgress"},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationItemStarted), "params": map[string]any{
					"threadId": "thread-remote", "turnId": "turn-remote",
					"item": map[string]any{"id": "cmd-1", "type": "commandExecution", "command": "ls", "status": "inProgress"},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationItemCompleted), "params": map[string]any{
					"threadId": "thread-remote", "turnId": "turn-remote",
					"item": map[string]any{"id": "cmd-1", "type": "commandExecution", "command": "ls", "aggregatedOutput": "ok\n", "exitCode": 0, "status": "completed"},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationItemCompleted), "params": map[string]any{
					"threadId": "thread-remote", "turnId": "turn-remote",
					"item": map[string]any{"id": "agent-1", "type": "agentMessage", "text": "done"},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationThreadTokenUsageUpdated), "params": map[string]any{
					"threadId": "thread-remote", "turnId": "turn-remote",
					"tokenUsage": map[string]any{
						"total": map[string]any{"inputTokens": 100, "cachedInputTokens": 40, "outputTokens": 10, "totalTokens": 110},
						"last":  map[string]any{"inputTokens": 100, "cachedInputTokens": 40, "outputTokens": 10, "totalTokens": 110},
					},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationTurnCompleted), "params": map[string]any{
					"threadId": "thread-remote",
					"turn": map[string]any{"id": "turn-remote", "status": "completed", "items": []any{
						map[string]any{"id": "agent-1", "type": "agentMessage", "text": "done"},
					}},
				}})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	lastMessagePath := filepath.Join(t.TempDir(), "last.txt")
	parsed, err := cli.Parse([]string{"--remote", endpoint, "exec", "--json", "-o", lastMessagePath, "hello"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	handled, err := runRemoteExecRequest(ctx, parsed, strings.NewReader(""), &stdout, &stderr)
	if err != nil || !handled {
		t.Fatalf("runRemoteExecRequest() = handled %v, error %v; stderr %s", handled, err, stderr.String())
	}

	events := decodeRemoteExecJSONLines(t, stdout.String())
	wantTypes := []string{"thread.started", "turn.started", "item.started", "item.completed", "item.completed", "turn.completed"}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %#v", events)
	}
	for i, want := range wantTypes {
		if events[i]["type"] != want {
			t.Fatalf("event[%d] type = %v, want %s (%#v)", i, events[i]["type"], want, events)
		}
	}
	if events[0]["thread_id"] != "thread-remote" {
		t.Fatalf("thread.started = %#v", events[0])
	}
	usage, _ := events[len(events)-1]["usage"].(map[string]any)
	if usage["input_tokens"] != float64(100) || usage["cached_input_tokens"] != float64(40) || usage["output_tokens"] != float64(10) {
		t.Fatalf("turn.completed usage = %#v", usage)
	}
	data, err := os.ReadFile(lastMessagePath)
	if err != nil {
		t.Fatalf("read last message file: %v", err)
	}
	if string(data) != "done" {
		t.Fatalf("last message file = %q", data)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

// TestRemoteExecResumesThreadLikeRust covers resuming by UUID with human output.
func TestRemoteExecResumesThreadLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 16)
	serverErrs := make(chan error, 1)
	const threadID = "9b2c3d4e-5f60-4718-9a0b-1c2d3e4f5a6b"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				if errors.Is(err, context.Canceled) || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			requests <- req
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadResume):
				var params appserver.ThreadResumeParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != threadID || !params.ExcludeTurns {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("thread/resume params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"thread": map[string]any{"id": threadID, "status": map[string]any{"type": "idle"}},
				}})
			case string(appserver.MethodTurnStart):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"turn": map[string]any{"id": "turn-resume", "items": []any{}, "status": "inProgress"},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationItemCompleted), "params": map[string]any{
					"threadId": threadID, "turnId": "turn-resume",
					"item": map[string]any{"id": "agent-2", "type": "agentMessage", "text": "resumed answer"},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationThreadTokenUsageUpdated), "params": map[string]any{
					"threadId": threadID, "turnId": "turn-resume",
					"tokenUsage": map[string]any{
						"total": map[string]any{"inputTokens": 30, "cachedInputTokens": 10, "outputTokens": 5, "totalTokens": 35},
					},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationTurnCompleted), "params": map[string]any{
					"threadId": threadID,
					"turn":     map[string]any{"id": "turn-resume", "status": "completed", "items": []any{}},
				}})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	parsed, err := cli.Parse([]string{"exec", "resume", "--remote", endpoint, threadID, "again"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	handled, err := runRemoteExecRequest(ctx, parsed, strings.NewReader(""), &stdout, &stderr)
	if err != nil || !handled {
		t.Fatalf("runRemoteExecRequest() = handled %v, error %v; stderr %s", handled, err, stderr.String())
	}
	if stdout.String() != "resumed answer\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	for _, want := range []string{"thread id: " + threadID, "codex\nresumed answer", "tokens used\n25\n"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q: %s", want, stderr.String())
		}
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestRemoteExecRejectsUnsupportedOptionsLikeRust(t *testing.T) {
	parsed, err := cli.Parse([]string{"exec", "--remote", "ws://127.0.0.1:4500", "-c", "model=gpt-5", "hello"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	handled, err := runRemoteExecRequest(context.Background(), parsed, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if !handled || err == nil {
		t.Fatalf("runRemoteExecRequest() = handled %v, error %v", handled, err)
	}
	if want := "--remote currently supports only prompt, resume by UUID, and output options; configure the agent on the server"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestRemoteExecRequiresResumeUUIDLikeRust(t *testing.T) {
	parsed, err := cli.Parse([]string{"exec", "resume", "--remote", "ws://127.0.0.1:4500", "not-a-uuid", "hello"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	handled, err := runRemoteExecRequest(context.Background(), parsed, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if !handled || err == nil || err.Error() != "remote resume requires a thread UUID" {
		t.Fatalf("runRemoteExecRequest() = handled %v, error %v", handled, err)
	}
}

func TestRemoteExecRejectsActiveThreadLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				return
			}
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadStart):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"thread": map[string]any{"id": "thread-busy", "status": map[string]any{"type": "active"}},
				}})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	parsed, err := cli.Parse([]string{"exec", "--remote", "ws" + strings.TrimPrefix(server.URL, "http"), "hello"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	_, err = runRemoteExecRequest(ctx, parsed, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "remote thread already has an active turn" {
		t.Fatalf("error = %v", err)
	}
}

func TestRemoteExecFailsWhenServerDisconnectsLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				return
			}
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadStart):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"thread": map[string]any{"id": "thread-drop", "status": map[string]any{"type": "idle"}},
				}})
			case string(appserver.MethodTurnStart):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"turn": map[string]any{"id": "turn-drop", "items": []any{}, "status": "inProgress"},
				}})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	parsed, err := cli.Parse([]string{"exec", "--remote", "ws" + strings.TrimPrefix(server.URL, "http"), "hello"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	_, err = runRemoteExecRequest(ctx, parsed, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "app-server disconnected before turn completion" {
		t.Fatalf("error = %v", err)
	}
}

// TestRemoteExecBackfillsLegacyCompletedTurnLikeRust covers Rust's
// maybe_backfill_turn_completed_items for a legacy thread: the completion
// carries no items, so exec re-reads the thread with its turns and renders the
// recovered final answer.
func TestRemoteExecBackfillsLegacyCompletedTurnLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				if errors.Is(err, context.Canceled) || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadStart):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"thread": map[string]any{"id": "thread-legacy", "status": map[string]any{"type": "idle"}, "historyMode": "legacy"},
				}})
			case string(appserver.MethodTurnStart):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"turn": map[string]any{"id": "turn-legacy", "items": []any{}, "status": "inProgress"},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationItemStarted), "params": map[string]any{
					"threadId": "thread-legacy", "turnId": "turn-legacy",
					"item": map[string]any{"id": "cmd-legacy", "type": "commandExecution", "command": "echo hi", "status": "inProgress"},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationTurnCompleted), "params": map[string]any{
					"threadId": "thread-legacy",
					"turn":     map[string]any{"id": "turn-legacy", "status": "completed", "items": []any{}, "itemsView": "notLoaded"},
				}})
			case string(appserver.MethodThreadRead):
				var params appserver.ThreadReadParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-legacy" || !params.IncludeTurns {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("thread/read params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"thread": map[string]any{
						"id": "thread-legacy", "status": map[string]any{"type": "idle"}, "historyMode": "legacy",
						"turns": []any{map[string]any{"id": "turn-legacy", "status": "completed", "items": []any{
							map[string]any{"id": "cmd-legacy", "type": "commandExecution", "command": "echo hi", "aggregatedOutput": "hi\n", "exitCode": 0, "status": "completed"},
							map[string]any{"id": "agent-legacy", "type": "agentMessage", "text": "legacy answer"},
						}}},
					},
				}})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	parsed, err := cli.Parse([]string{"exec", "--remote", "ws" + strings.TrimPrefix(server.URL, "http"), "hello"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	handled, err := runRemoteExecRequest(ctx, parsed, strings.NewReader(""), &stdout, &stderr)
	if err != nil || !handled {
		t.Fatalf("runRemoteExecRequest() = handled %v, error %v; stderr %s", handled, err, stderr.String())
	}
	if stdout.String() != "legacy answer\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

// TestRemoteExecBackfillsPaginatedCompletedTurnLikeRust covers the paginated
// half of Rust's backfill: thread/items/list is paged for the completed turn and
// the recovered items reach the JSONL stream.
func TestRemoteExecBackfillsPaginatedCompletedTurnLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				if errors.Is(err, context.Canceled) || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadStart):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"thread": map[string]any{"id": "thread-paged", "status": map[string]any{"type": "idle"}, "historyMode": "paginated"},
				}})
			case string(appserver.MethodTurnStart):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"turn": map[string]any{"id": "turn-paged", "items": []any{}, "status": "inProgress"},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationItemStarted), "params": map[string]any{
					"threadId": "thread-paged", "turnId": "turn-paged",
					"item": map[string]any{"id": "cmd-paged", "type": "commandExecution", "command": "echo hi", "status": "inProgress"},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationTurnCompleted), "params": map[string]any{
					"threadId": "thread-paged",
					"turn":     map[string]any{"id": "turn-paged", "status": "completed", "items": []any{}, "itemsView": "summary"},
				}})
			case string(appserver.MethodThreadItemsList):
				var params appserver.ThreadItemsListParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-paged" || params.TurnID == nil || *params.TurnID != "turn-paged" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("thread/items/list params = %#v", params))
					return
				}
				if params.Cursor == nil || params.Cursor.Opaque == "" {
					next := "page-2"
					remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
						"data": []any{map[string]any{"turnId": "turn-paged", "item": map[string]any{
							"id": "agent-paged", "type": "agentMessage", "text": "paged answer",
						}}},
						"nextCursor": next,
					}})
					continue
				}
				if params.Cursor.Opaque != "page-2" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("thread/items/list cursor = %#v", params.Cursor))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"data": []any{map[string]any{"turnId": "turn-paged", "item": map[string]any{
						"id": "cmd-paged", "type": "commandExecution", "command": "echo hi", "aggregatedOutput": "hi\n", "exitCode": 0, "status": "completed",
					}}},
					"nextCursor": nil,
				}})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	lastMessagePath := filepath.Join(t.TempDir(), "last.txt")
	parsed, err := cli.Parse([]string{"exec", "--remote", "ws" + strings.TrimPrefix(server.URL, "http"), "--json", "-o", lastMessagePath, "hello"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	handled, err := runRemoteExecRequest(ctx, parsed, strings.NewReader(""), &stdout, &stderr)
	if err != nil || !handled {
		t.Fatalf("runRemoteExecRequest() = handled %v, error %v; stderr %s", handled, err, stderr.String())
	}
	data, err := os.ReadFile(lastMessagePath)
	if err != nil {
		t.Fatalf("read last message file: %v", err)
	}
	if string(data) != "paged answer" {
		t.Fatalf("last message file = %q", data)
	}
	sawCommand := false
	for _, event := range decodeRemoteExecJSONLines(t, stdout.String()) {
		item, _ := event["item"].(map[string]any)
		if event["type"] == "item.completed" && item["type"] == "command_execution" {
			sawCommand = true
		}
	}
	if !sawCommand {
		t.Fatalf("JSONL missing the backfilled command item: %s", stdout.String())
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

// TestRemoteExecFailedTurnOutputLikeRust covers Rust's split failure output:
// the human ERROR line keeps TurnError's Display message while the JSONL
// turn.failed event appends the additional details.
func TestRemoteExecFailedTurnOutputLikeRust(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", jsonOutput), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			serverErrs := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				defer conn.Close(websocket.StatusNormalClosure, "")
				for {
					req, err := remoteTUITestReadRequest(ctx, conn)
					if err != nil {
						if errors.Is(err, context.Canceled) || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
							return
						}
						remoteTUITestSendErr(serverErrs, err)
						return
					}
					switch req.Method {
					case string(appserver.MethodInitialize):
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
					case string(appserver.MethodThreadStart):
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
							"thread": map[string]any{"id": "thread-failed", "status": map[string]any{"type": "idle"}},
						}})
					case string(appserver.MethodTurnStart):
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
							"turn": map[string]any{"id": "turn-failed", "items": []any{}, "status": "inProgress"},
						}})
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationTurnCompleted), "params": map[string]any{
							"threadId": "thread-failed",
							"turn": map[string]any{"id": "turn-failed", "status": "failed", "items": []any{},
								"error": map[string]any{"message": "boom", "additionalDetails": "trace"}},
						}})
					default:
						remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
						return
					}
				}
			}))
			defer server.Close()

			args := []string{"exec", "--remote", "ws" + strings.TrimPrefix(server.URL, "http"), "hello"}
			if jsonOutput {
				args = []string{"exec", "--remote", "ws" + strings.TrimPrefix(server.URL, "http"), "--json", "hello"}
			}
			parsed, err := cli.Parse(args)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			var stdout, stderr bytes.Buffer
			handled, err := runRemoteExecRequest(ctx, parsed, strings.NewReader(""), &stdout, &stderr)
			if !handled {
				t.Fatal("runRemoteExecRequest() did not handle the remote exec")
			}
			var exitErr *ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != 1 {
				t.Fatalf("error = %v, want exit code 1", err)
			}
			if jsonOutput {
				found := false
				for _, event := range decodeRemoteExecJSONLines(t, stdout.String()) {
					errorPayload, _ := event["error"].(map[string]any)
					if event["type"] == "turn.failed" && errorPayload["message"] == "boom (trace)" {
						found = true
					}
				}
				if !found {
					t.Fatalf("JSONL missing turn.failed with details: %s", stdout.String())
				}
				return
			}
			if !strings.Contains(stderr.String(), "ERROR: boom\n") {
				t.Fatalf("stderr = %q", stderr.String())
			}
			if strings.Contains(stderr.String(), "trace") {
				t.Fatalf("human ERROR line must not include additional details: %q", stderr.String())
			}
			select {
			case err := <-serverErrs:
				t.Fatalf("server error: %v", err)
			default:
			}
		})
	}
}

func decodeRemoteExecJSONLines(t *testing.T, text string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(text), "\n")
	events := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode JSONL %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}
