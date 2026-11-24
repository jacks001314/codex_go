package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/turn"
)

// TestRemoteExecForwardsModelAndEffortOverridesLikeRust covers Rust #63462:
// `--model` and `-c model_reasoning_effort=...` reach the remote thread, and
// `--model` wins over `-c model=...`.
func TestRemoteExecForwardsModelAndEffortOverridesLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var startParams appserver.ThreadStartParams
	var turnParams turn.TurnStartParams
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
				if err := json.Unmarshal(req.Params, &startParams); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"thread": map[string]any{"id": "thread-overrides", "status": map[string]any{"type": "idle"}},
				}})
			case string(appserver.MethodTurnStart):
				mu.Lock()
				err := json.Unmarshal(req.Params, &turnParams)
				mu.Unlock()
				if err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"turn": map[string]any{"id": "turn-overrides", "items": []any{}, "status": "inProgress"},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationItemCompleted), "params": map[string]any{
					"threadId": "thread-overrides", "turnId": "turn-overrides",
					"item": map[string]any{"id": "agent-1", "type": "agentMessage", "text": "ok"},
				}})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "method": string(appserver.NotificationTurnCompleted), "params": map[string]any{
					"threadId": "thread-overrides",
					"turn":     map[string]any{"id": "turn-overrides", "status": "completed", "items": []any{}},
				}})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	parsed, err := cli.Parse([]string{"exec", "--remote", endpoint, "-m", "cli-model", "-c", "model=config-model", "-c", "model_reasoning_effort=high", "hello"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	handled, err := runRemoteExecRequest(ctx, parsed, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || !handled {
		t.Fatalf("runRemoteExecRequest() = handled %v, error %v", handled, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if startParams.Model != "cli-model" {
		t.Fatalf("thread/start model = %q, want --model to win", startParams.Model)
	}
	if turnParams.Model != "cli-model" {
		t.Fatalf("turn/start model = %q, want the override on the turn", turnParams.Model)
	}
	if turnParams.Effort == nil || *turnParams.Effort != "high" {
		t.Fatalf("turn/start effort = %v, want high", turnParams.Effort)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

// TestRemoteExecResumeForwardsOverridesOnTurnLikeRust keeps the resumed thread's
// server settings intact: the overrides travel on turn/start only.
func TestRemoteExecResumeForwardsOverridesOnTurnLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const threadID = "9b2c3d4e-5f60-4718-9a0b-1c2d3e4f5a6b"
	var mu sync.Mutex
	var resumeParams appserver.ThreadResumeParams
	var turnParams turn.TurnStartParams
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
			case string(appserver.MethodThreadResume):
				mu.Lock()
				err := json.Unmarshal(req.Params, &resumeParams)
				mu.Unlock()
				if err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"thread": map[string]any{"id": threadID, "status": map[string]any{"type": "idle"}},
				}})
			case string(appserver.MethodTurnStart):
				mu.Lock()
				err := json.Unmarshal(req.Params, &turnParams)
				mu.Unlock()
				if err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"turn": map[string]any{"id": "turn-resume", "items": []any{}, "status": "inProgress"},
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
	parsed, err := cli.Parse([]string{"exec", "resume", "--remote", endpoint, "-c", "model=resume-model", "-c", "model_reasoning_effort=low", threadID, "again"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	handled, err := runRemoteExecRequest(ctx, parsed, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || !handled {
		t.Fatalf("runRemoteExecRequest() = handled %v, error %v", handled, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if resumeParams.ThreadID != threadID {
		t.Fatalf("thread/resume params = %#v", resumeParams)
	}
	if turnParams.Model != "resume-model" {
		t.Fatalf("turn/start model = %q, want resume-model", turnParams.Model)
	}
	if turnParams.Effort == nil || *turnParams.Effort != "low" {
		t.Fatalf("turn/start effort = %v, want low", turnParams.Effort)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

// TestRemoteExecRejectsInvalidOverridesLikeRust checks the override diagnostics
// before any connection to the app server is made.
func TestRemoteExecRejectsInvalidOverridesLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		args []string
		want string
	}{
		{args: []string{"-c", "model=5"}, want: "invalid model override: expected a string"},
		{args: []string{"-c", "model_reasoning_effort=3"}, want: "invalid model_reasoning_effort override"},
		{args: []string{"-c", "model_reasoning_effort="}, want: "invalid model_reasoning_effort override"},
		{args: []string{"-c", "approval_policy=on-request"}, want: "--remote does not support config override `approval_policy`; configure it on the server"},
	} {
		args := append([]string{"exec", "--remote", "ws://127.0.0.1:4500"}, testCase.args...)
		args = append(args, "hello")
		parsed, err := cli.Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v) error = %v", args, err)
		}
		handled, err := runRemoteExecRequest(context.Background(), parsed, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		if !handled || err == nil {
			t.Fatalf("runRemoteExecRequest(%v) = handled %v, error %v", testCase.args, handled, err)
		}
		if err.Error() != testCase.want {
			t.Fatalf("error for %v = %q, want %q", testCase.args, err.Error(), testCase.want)
		}
	}
}
