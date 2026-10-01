package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

// TestRemoteEnsureThreadCommandStartsThreadLikeRust covers the /goal bootstrap
// for a daemon- or remote-backed TUI: /goal has no thread id until the session
// starts, so the TUI asks the host to start the session thread without a turn
// (Rust starts that thread during startup and queues /goal until it exists).
func TestRemoteEnsureThreadCommandStartsThreadLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	startRequests := 0
	socketPath := startUnixWebSocketTestServer(t, func(_ context.Context, conn *websocket.Conn) {
		for {
			request, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				return
			}
			switch request.Method {
			case string(appserver.MethodThreadStart):
				startRequests++
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      request.ID,
					"result":  map[string]any{"thread": map[string]any{"id": "thread-ensured"}},
				})
			default:
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
			}
		}
	})

	state := codextui.NewState(nil)
	command := interactiveRemoteEnsureThreadCommand(ctx, &cli.RootOptions{}, appserverdaemon.NewUnixSocketEndpoint(socketPath), state, remoteTUIBrokers{}, nil)
	if command == nil {
		t.Fatal("ensure-thread command is nil")
	}
	message := command()
	started, ok := message.(codextea.StreamStartedMsg)
	if !ok {
		t.Fatalf("ensure-thread message = %#v, want a stream", message)
	}

	var threadStarted bool
	for streamed := range started.Messages {
		event, ok := streamed.(codextea.ThreadEventMsg)
		if !ok {
			continue
		}
		if event.Event.Type == "thread.started" && event.Event.ThreadID == "thread-ensured" {
			threadStarted = true
		}
		if event.Event.Error != nil {
			t.Fatalf("ensure-thread stream error = %s", event.Event.Error.Message)
		}
	}
	if !threadStarted {
		t.Fatal("ensure-thread stream did not report the started thread")
	}
	if startRequests != 1 {
		t.Fatalf("thread/start requests = %d, want 1", startRequests)
	}
}

// TestRemoteEnsureThreadCommandReportsFailureWithoutTurn keeps the failure path
// free of a fabricated turn completion: no turn was requested.
func TestRemoteEnsureThreadCommandReportsFailureWithoutTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	socketPath := startUnixWebSocketTestServer(t, func(_ context.Context, conn *websocket.Conn) {
		for {
			request, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				return
			}
			if request.Method == string(appserver.MethodThreadStart) {
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      request.ID,
					"error":   map[string]any{"code": -32603, "message": "goals are disabled"},
				})
				continue
			}
			remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
		}
	})

	state := codextui.NewState(nil)
	command := interactiveRemoteEnsureThreadCommand(ctx, &cli.RootOptions{}, appserverdaemon.NewUnixSocketEndpoint(socketPath), state, remoteTUIBrokers{}, nil)
	started, ok := command().(codextea.StreamStartedMsg)
	if !ok {
		t.Fatal("ensure-thread command did not return a stream")
	}
	var failure string
	for streamed := range started.Messages {
		if _, isCompletion := streamed.(codextea.TurnCompletedMsg); isCompletion {
			t.Fatal("ensure-thread reported a turn completion for a session start")
		}
		event, ok := streamed.(codextea.ThreadEventMsg)
		if !ok || event.Event.Error == nil {
			continue
		}
		failure = event.Event.Error.Message
	}
	if failure == "" {
		t.Fatal("ensure-thread failure was not reported")
	}
}

// TestRemoteGoalContinuationTurnParamsLikeRust pins the wire shape of a
// TUI-driven goal continuation: the turn carries the "goal" trigger and the
// continuation prompt as additional context instead of user input, exactly
// like the host-side continuation (Rust ext/goal runtime.rs).
func TestRemoteGoalContinuationTurnParamsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	params, err := remoteTurnStartParams(&cli.RootOptions{}, state, "thread-goal", codextea.SubmitRequest{
		TurnTrigger: "goal",
		AdditionalContext: map[string]any{
			"goal": map[string]any{"kind": "application", "value": "continue toward the objective"},
		},
	})
	if err != nil {
		t.Fatalf("remoteTurnStartParams: %v", err)
	}
	if params.TurnTrigger != "goal" {
		t.Fatalf("turnTrigger = %q, want goal", params.TurnTrigger)
	}
	if len(params.Input) != 0 {
		t.Fatalf("goal continuation input = %#v, want none", params.Input)
	}
	entry, ok := params.AdditionalContext["goal"]
	if !ok || entry.Value != "continue toward the objective" || entry.Kind != turn.AdditionalContextApplication {
		t.Fatalf("goal additional context = %#v", params.AdditionalContext)
	}
	// A plain user turn still requires input and keeps the "user" trigger.
	if _, err := remoteTurnStartParams(&cli.RootOptions{}, state, "thread-goal", codextea.SubmitRequest{}); err == nil {
		t.Fatal("empty user turn was accepted")
	}
}

// TestRemoteGoalContinuationCommandStartsGoalTurnLikeRust drives the whole
// continuation command against a stub app server: the session thread is kept,
// and the started turn is the goal continuation.
func TestRemoteGoalContinuationCommandStartsGoalTurnLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	turnStarts := make(chan turn.TurnStartParams, 1)
	socketPath := startUnixWebSocketTestServer(t, func(_ context.Context, conn *websocket.Conn) {
		for {
			request, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				return
			}
			switch request.Method {
			case string(appserver.MethodTurnStart):
				var params turn.TurnStartParams
				if err := json.Unmarshal(request.Params, &params); err != nil {
					remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32602, "message": err.Error()}})
					continue
				}
				turnStarts <- params
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      request.ID,
					"result":  map[string]any{"turn": map[string]any{"id": "turn-goal", "status": "inProgress"}},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationTurnCompleted),
					"params":  map[string]any{"threadId": "thread-goal", "turn": map[string]any{"id": "turn-goal", "items": []any{}, "status": "completed"}},
				})
			default:
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
			}
		}
	})

	state := codextui.NewState(nil)
	state.SetThreadID("thread-goal")
	command := interactiveRemoteGoalContinuationCommand(ctx, &cli.RootOptions{}, appserverdaemon.NewUnixSocketEndpoint(socketPath), state, remoteTUIBrokers{}, nil, nil)
	continuation := command(appserver.Goal{ThreadID: "thread-goal", Objective: "finish the goal", Status: appserver.GoalActive})
	if continuation == nil {
		t.Fatal("goal continuation command is nil")
	}
	started, ok := continuation().(codextea.StreamStartedMsg)
	if !ok {
		t.Fatal("goal continuation did not return a stream")
	}
	completed := false
	for streamed := range started.Messages {
		message, ok := streamed.(codextea.TurnCompletedMsg)
		if !ok {
			continue
		}
		if message.Err != nil {
			t.Fatalf("goal continuation turn error = %v", message.Err)
		}
		completed = true
	}
	if !completed {
		t.Fatal("goal continuation never completed")
	}

	select {
	case params := <-turnStarts:
		if params.TurnTrigger != "goal" {
			t.Fatalf("turnTrigger = %q, want goal", params.TurnTrigger)
		}
		if params.ThreadID != "thread-goal" {
			t.Fatalf("threadId = %q", params.ThreadID)
		}
		if len(params.Input) != 0 {
			t.Fatalf("continuation input = %#v, want none", params.Input)
		}
		if _, ok := params.AdditionalContext["goal"]; !ok {
			t.Fatalf("continuation additional context = %#v", params.AdditionalContext)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("goal continuation never started a turn")
	}
}
